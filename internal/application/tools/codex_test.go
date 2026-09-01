package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"github.com/phlin/go-agent/internal/config"
)

func TestCodexToolUsesAppServerProtocol(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "codex")
	script := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*) echo '{"id":0,"result":{}}' ;;
    *'"method":"thread/start"'*) echo '{"id":1,"result":{"thread":{"id":"thr_test"}}}' ;;
    *'"method":"turn/start"'*)
      echo '{"id":2,"result":{"turn":{"id":"turn_test","status":"inProgress"}}}'
      echo '{"method":"item/agentMessage/delta","params":{"delta":"done"}}'
      echo '{"method":"turn/completed","params":{"turn":{"status":"completed"}}}'
      ;;
  esac
done
`
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	base := NewCodexToolWithApproval(config.CodexConfig{Enabled: true, Binary: fake, CWD: dir, Timeout: "2s"}, nil)
	invokable, ok := base.(tool.InvokableTool)
	if !ok {
		t.Fatal("Codex tool is not invokable")
	}
	result, err := invokable.InvokableRun(context.Background(), `{"task":"inspect the repo"}`)
	if err != nil {
		t.Fatalf("run Codex tool: %v", err)
	}
	if !strings.Contains(result, `"answer":"done"`) {
		t.Fatalf("unexpected result: %s", result)
	}
}

func TestWriteApprovalRequiresWhitelistAndOnlyDangerousTasksConfirm(t *testing.T) {
	store := NewWriteApprovalStore(time.Minute)
	base := NewCodexToolWithApproval(config.CodexConfig{Enabled: true, Binary: "does-not-exist", CWD: t.TempDir()}, store, []int64{42})
	invokable := base.(tool.InvokableTool)

	ctx := withToolIdentity(context.Background(), 7, 99)
	forbidden, err := invokable.InvokableRun(ctx, `{"task":"edit main.go", "write":true}`)
	if err != nil || !strings.Contains(forbidden, `"write_forbidden"`) {
		t.Fatalf("expected non-whitelisted user to be blocked: %s, %v", forbidden, err)
	}

	ctx = withToolIdentity(context.Background(), 7, 42)
	_, err = invokable.InvokableRun(ctx, `{"task":"edit main.go", "write":true}`)
	if err == nil || strings.Contains(err.Error(), "confirmation_required") || strings.Contains(err.Error(), "write_forbidden") {
		t.Fatalf("whitelisted ordinary edit should bypass confirmation and reach app-server: %v", err)
	}
	_, err = invokable.InvokableRun(ctx, `{"task":"delete old file", "write":true}`)
	if err != nil {
		t.Fatal(err)
	}
	store.ObserveConfirmation(7, 42, "确认", time.Now())
	// The fake binary is intentionally absent; reaching it proves confirmation
	// was accepted rather than returning confirmation_required again.
	_, err = invokable.InvokableRun(ctx, `{"task":"delete old file", "write":true}`)
	if err == nil || strings.Contains(err.Error(), "confirmation_required") {
		t.Fatalf("expected confirmed task to reach app-server: %v", err)
	}
}

func TestWriteApprovalStoreBindsUserGroupTaskAndConsumesOnce(t *testing.T) {
	now := time.Now()
	store := NewWriteApprovalStore(time.Minute)
	store.Request(1, 2, "edit main.go", now)
	store.ObserveConfirmation(1, 3, "确认", now)
	if store.Consume(1, 2, "edit main.go", now) {
		t.Fatal("different user must not confirm")
	}
	store.ObserveConfirmation(1, 2, "好的，确认一下", now)
	if store.Consume(1, 2, "edit other.go", now) {
		t.Fatal("different task must not consume approval")
	}
	store.ObserveConfirmation(1, 2, "确认", now)
	if !store.Consume(1, 2, "edit main.go", now) || store.Consume(1, 2, "edit main.go", now) {
		t.Fatal("approval should be explicit and one-shot")
	}
}

func TestWriteApprovalStoreExpires(t *testing.T) {
	now := time.Now()
	store := NewWriteApprovalStore(time.Second)
	store.Request(1, 2, "edit main.go", now)
	store.ObserveConfirmation(1, 2, "确认", now)
	if store.Consume(1, 2, "edit main.go", now.Add(2*time.Second)) {
		t.Fatal("expired approval must not be consumed")
	}
}

func TestDangerousWriteTaskClassification(t *testing.T) {
	for _, task := range []string{"edit main.go", "update config", "修复一个函数"} {
		if dangerousWriteTask(task) {
			t.Fatalf("ordinary task classified as dangerous: %q", task)
		}
	}
	for _, task := range []string{"delete old file", "覆盖配置", "run shell command", "update .env token"} {
		if !dangerousWriteTask(task) {
			t.Fatalf("dangerous task not classified: %q", task)
		}
	}
}

func TestMCPToolNameIsNamespaced(t *testing.T) {
	if got := mcpToolName("Web Search", "news/latest"); got != "mcp_web_search_news_latest" {
		t.Fatalf("unexpected MCP tool name %q", got)
	}
}
