package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/phlin/go-agent/internal/config"
)

type codexTool struct {
	cfg        config.CodexConfig
	slots      chan struct{}
	approvals  *WriteApprovalStore
	writeUsers map[int64]struct{}
}

type codexArgs struct {
	Task  string `json:"task"`
	Write bool   `json:"write"`
}

func NewCodexTool(cfg config.CodexConfig) tool.BaseTool {
	return newCodexTool(cfg, nil, nil)
}

func NewCodexToolWithApproval(cfg config.CodexConfig, approvals *WriteApprovalStore, writeUsers ...[]int64) tool.BaseTool {
	var allowed []int64
	if len(writeUsers) > 0 {
		allowed = writeUsers[0]
	}
	return newCodexTool(cfg, approvals, allowed)
}

func newCodexTool(cfg config.CodexConfig, approvals *WriteApprovalStore, writeUsers []int64) tool.BaseTool {
	if !cfg.Enabled {
		return nil
	}
	if cfg.Binary == "" {
		cfg.Binary = "codex"
	}
	if cfg.CWD == "" {
		cfg.CWD, _ = os.Getwd()
	}
	if absolute, err := filepath.Abs(cfg.CWD); err == nil {
		cfg.CWD = absolute
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 1
	}
	allowed := make(map[int64]struct{}, len(writeUsers))
	for _, userID := range writeUsers {
		if userID != 0 {
			allowed[userID] = struct{}{}
		}
	}
	return &codexTool{cfg: cfg, slots: make(chan struct{}, cfg.MaxConcurrency), approvals: approvals, writeUsers: allowed}
}

func (t *codexTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "delegate_codex_task",
		Desc: "Delegate a complex local task to Codex app-server. Defaults to read-only. Set write=true for a project edit; only QQ users in the write whitelist may use it. Destructive edits still require explicit QQ confirmation, and all writes are limited to the configured project cwd.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"task":  {Type: schema.String, Required: true, Desc: "A self-contained task with the desired outcome and relevant constraints."},
			"write": {Type: schema.Boolean, Desc: "Request file writes. Requires the caller to be in the configured QQ whitelist; destructive tasks also require explicit confirmation."},
		}),
	}, nil
}

func (t *codexTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args codexArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("decode Codex task: %w", err)
	}
	if strings.TrimSpace(args.Task) == "" {
		return "", errors.New("Codex task is required")
	}
	groupID, userID, hasIdentity := toolIdentity(ctx)
	if args.Write {
		if _, ok := t.writeUsers[userID]; !ok {
			return marshal(map[string]string{"status": "write_forbidden", "message": "只有 Codex 写权限 QQ 白名单内的用户可以修改项目文件。"})
		}
		// Trusted users can run ordinary workspace edits directly. Destructive
		// requests still require an explicit QQ confirmation for the exact task.
		if dangerousWriteTask(args.Task) {
			if !hasIdentity || t.approvals == nil || !t.approvals.Consume(groupID, userID, args.Task, time.Now()) {
				if !hasIdentity || t.approvals == nil {
					return marshal(map[string]string{"status": "confirmation_required", "message": "该写入任务可能有破坏性影响，请先在 QQ 中明确回复“确认”或“允许”。"})
				}
				message := t.approvals.Request(groupID, userID, args.Task, time.Now())
				return marshal(map[string]string{"status": "confirmation_required", "message": message})
			}
		}
	}
	select {
	case t.slots <- struct{}{}:
		defer func() { <-t.slots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}

	timeout := parseToolDuration(t.cfg.Timeout, 5*time.Minute)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	answer, err := runCodexAppServer(runCtx, t.cfg, args.Task, args.Write)
	if err != nil {
		return "", err
	}
	return marshal(map[string]string{"answer": answer})
}

func dangerousWriteTask(task string) bool {
	task = strings.ToLower(strings.TrimSpace(task))
	for _, marker := range []string{
		"rm -", "rmdir", "remove", "delete", "删", "删除", "覆盖", "overwrite",
		"format", "chmod", "chown", "kill", "secret", "credential", "token",
		"password", ".env", "密钥", "密码", "批量重命名", "execute", "exec ",
		"shell", "command", "script", "脚本", "命令", "联网", "network",
		"download", "install", "安装",
	} {
		if strings.Contains(task, marker) {
			return true
		}
	}
	return false
}

type appServerMessage struct {
	ID     *int            `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func runCodexAppServer(ctx context.Context, cfg config.CodexConfig, task string, writeRequest ...bool) (string, error) {
	write := len(writeRequest) > 0 && writeRequest[0]
	cmd := exec.CommandContext(ctx, cfg.Binary, "app-server")
	cmd.Dir = cfg.CWD
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start Codex app-server: %w", err)
	}
	defer stopProcess(cmd, stdin)

	encoder := json.NewEncoder(stdin)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	send := func(method string, id *int, params any) error {
		message := map[string]any{"method": method, "params": params}
		if id != nil {
			message["id"] = *id
		}
		return encoder.Encode(message)
	}
	readResponse := func(id int) (appServerMessage, error) {
		for scanner.Scan() {
			var message appServerMessage
			if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
				return appServerMessage{}, fmt.Errorf("decode Codex app-server response: %w", err)
			}
			if message.ID == nil || *message.ID != id {
				continue
			}
			if message.Error != nil {
				return appServerMessage{}, fmt.Errorf("Codex app-server error %d: %s", message.Error.Code, message.Error.Message)
			}
			return message, nil
		}
		return appServerMessage{}, appServerReadError(ctx, scanner.Err(), stderr.String())
	}

	id := 0
	if err := send("initialize", &id, map[string]any{"clientInfo": map[string]string{
		"name": "qq_group_bot", "title": "QQ Group Bot", "version": "1.0.0",
	}}); err != nil {
		return "", err
	}
	if _, err := readResponse(id); err != nil {
		return "", err
	}
	if err := send("initialized", nil, map[string]any{}); err != nil {
		return "", err
	}

	sandbox := "readOnly"
	if write {
		sandbox = "workspaceWrite"
	}
	threadParams := map[string]any{
		"cwd": cfg.CWD, "approvalPolicy": "never", "sandbox": sandbox,
		"serviceName": "qq_group_bot", "ephemeral": true,
	}
	if cfg.Model != "" {
		threadParams["model"] = cfg.Model
	}
	id++
	if err := send("thread/start", &id, threadParams); err != nil {
		return "", err
	}
	threadResponse, err := readResponse(id)
	if err != nil {
		return "", err
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(threadResponse.Result, &started); err != nil || started.Thread.ID == "" {
		return "", errors.New("Codex app-server returned no thread id")
	}

	id++
	// Network access remains an explicit operator configuration for both
	// read-only and write-capable turns.
	sandboxPolicy := map[string]any{"type": sandbox, "networkAccess": cfg.NetworkEnabled}
	if write {
		sandboxPolicy["writableRoots"] = []string{cfg.CWD}
	}
	if err := send("turn/start", &id, map[string]any{
		"threadId":       started.Thread.ID,
		"input":          []map[string]string{{"type": "text", "text": task}},
		"approvalPolicy": "never",
		"sandboxPolicy":  sandboxPolicy,
	}); err != nil {
		return "", err
	}
	if _, err := readResponse(id); err != nil {
		return "", err
	}

	var answer strings.Builder
	for scanner.Scan() {
		var message appServerMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			return "", fmt.Errorf("decode Codex notification: %w", err)
		}
		switch message.Method {
		case "item/agentMessage/delta":
			var params struct {
				Delta string `json:"delta"`
			}
			if json.Unmarshal(message.Params, &params) == nil {
				answer.WriteString(params.Delta)
			}
		case "item/completed":
			if answer.Len() == 0 {
				var params struct {
					Item struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"item"`
				}
				if json.Unmarshal(message.Params, &params) == nil && params.Item.Type == "agentMessage" {
					answer.WriteString(params.Item.Text)
				}
			}
		case "turn/completed":
			var params struct {
				Turn struct {
					Status string `json:"status"`
					Error  *struct {
						Message string `json:"message"`
					} `json:"error"`
				} `json:"turn"`
			}
			_ = json.Unmarshal(message.Params, &params)
			if params.Turn.Status != "completed" {
				if params.Turn.Error != nil {
					return "", errors.New(params.Turn.Error.Message)
				}
				return "", fmt.Errorf("Codex turn ended with status %q", params.Turn.Status)
			}
			result := strings.TrimSpace(answer.String())
			if result == "" {
				return "", errors.New("Codex completed without an answer")
			}
			return truncateRunes(result, 12_000), nil
		}
	}
	return "", appServerReadError(ctx, scanner.Err(), stderr.String())
}

func appServerReadError(ctx context.Context, scanErr error, stderr string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if scanErr != nil {
		return scanErr
	}
	if stderr = strings.TrimSpace(stderr); stderr != "" {
		return fmt.Errorf("Codex app-server stopped: %s", truncateRunes(stderr, 2_000))
	}
	return io.ErrUnexpectedEOF
}

func stopProcess(cmd *exec.Cmd, stdin io.Closer) {
	_ = stdin.Close()
	if cmd.Process != nil && cmd.ProcessState == nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
