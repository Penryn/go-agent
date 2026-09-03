package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino-ext/components/tool/mcp/officialmcp"
	"github.com/cloudwego/eino/components/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/phlin/go-agent/internal/config"
)

type MCPTools struct {
	Tools    []tool.BaseTool
	sessions []*mcp.ClientSession
}

type MCPToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// MCPManager owns the replaceable MCP connection set. It never mutates the
// active set until the replacement has connected and its tools are registered.
type MCPManager struct {
	mu      sync.RWMutex
	applyMu sync.Mutex
	runtime *Runtime
	active  *MCPTools
	servers []config.MCPServerConfig
}

func NewMCPManager(runtime *Runtime, active *MCPTools, servers []config.MCPServerConfig) *MCPManager {
	return &MCPManager{runtime: runtime, active: active, servers: cloneMCPServers(servers)}
}

func (m *MCPManager) Servers() []config.MCPServerConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneMCPServers(m.servers)
}

func (m *MCPManager) ToolInfos(ctx context.Context) ([]MCPToolInfo, error) {
	m.mu.RLock()
	active := m.active
	m.mu.RUnlock()
	if active == nil {
		return []MCPToolInfo{}, nil
	}
	result := make([]MCPToolInfo, 0, len(active.Tools))
	for _, candidate := range active.Tools {
		info, err := candidate.Info(ctx)
		if err != nil {
			return nil, err
		}
		result = append(result, MCPToolInfo{Name: info.Name, Description: info.Desc})
	}
	return result, nil
}

func (m *MCPManager) Apply(ctx context.Context, servers []config.MCPServerConfig) error {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	next, err := ConnectMCP(ctx, servers)
	if err != nil {
		return err
	}
	if err := m.runtime.ReplaceMCPTools(ctx, next.Tools...); err != nil {
		_ = next.Close()
		return err
	}
	m.mu.Lock()
	old := m.active
	m.active = next
	m.servers = cloneMCPServers(servers)
	m.mu.Unlock()
	if old != nil {
		if err := old.Close(); err != nil {
			slog.Warn("close previous MCP sessions", "error", err)
		}
	}
	return nil
}

func (m *MCPManager) Close() error {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	m.mu.Lock()
	active := m.active
	m.active = nil
	m.mu.Unlock()
	if active == nil {
		return nil
	}
	return active.Close()
}

func cloneMCPServers(servers []config.MCPServerConfig) []config.MCPServerConfig {
	result := make([]config.MCPServerConfig, len(servers))
	copy(result, servers)
	for i := range result {
		result[i].Args = append([]string(nil), result[i].Args...)
		result[i].Tools = append([]string(nil), result[i].Tools...)
	}
	return result
}

func ConnectMCP(ctx context.Context, servers []config.MCPServerConfig) (*MCPTools, error) {
	set := &MCPTools{}
	for _, server := range servers {
		if !server.Enabled {
			continue
		}
		loaded, session, err := connectMCPServer(ctx, server)
		if err != nil {
			if server.Required {
				_ = set.Close()
				return nil, fmt.Errorf("connect required MCP server %q: %w", server.Name, err)
			}
			slog.Warn("optional MCP server unavailable", "server", server.Name, "error", err)
			continue
		}
		set.Tools = append(set.Tools, loaded...)
		set.sessions = append(set.sessions, session)
		slog.Info("MCP tools loaded", "server", server.Name, "tools", len(loaded))
	}
	return set, nil
}

func connectMCPServer(ctx context.Context, cfg config.MCPServerConfig) ([]tool.BaseTool, *mcp.ClientSession, error) {
	var transport mcp.Transport
	switch cfg.Transport {
	case "stdio":
		cmd := exec.Command(cfg.Command, cfg.Args...)
		cmd.Stderr = os.Stderr
		transport = &mcp.CommandTransport{Command: cmd}
	case "http":
		transport = &mcp.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: &http.Client{}}
	default:
		return nil, nil, fmt.Errorf("unsupported transport %q", cfg.Transport)
	}

	timeout := parseToolDuration(cfg.Timeout, 15*time.Second)
	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "qq-group-bot", Version: "1.0.0"}, nil)
	session, err := client.Connect(connectCtx, transport, nil)
	if err != nil {
		return nil, nil, err
	}
	loaded, err := officialmcp.GetTools(connectCtx, &officialmcp.Config{
		Cli:           session,
		ServerName:    cfg.Name,
		ToolNameList:  cfg.Tools,
		ListToolsMode: officialmcp.ListToolsAllPages,
		ToolNameMapper: func(_ context.Context, input officialmcp.ToolNameMapperInput) (officialmcp.ToolNameMapperOutput, error) {
			return officialmcp.ToolNameMapperOutput{ExposedName: mcpToolName(input.ServerName, input.Tool.Name)}, nil
		},
		ResultPolicy: &officialmcp.ResultPolicy{MaxChars: 12_000, PreserveTailChars: 1_000},
	})
	if err != nil {
		_ = session.Close()
		return nil, nil, err
	}
	return loaded, session, nil
}

func (m *MCPTools) Close() error {
	if m == nil {
		return nil
	}
	var errs []error
	for _, session := range m.sessions {
		errs = append(errs, session.Close())
	}
	return errors.Join(errs...)
}

func mcpToolName(server, name string) string {
	raw := "mcp_" + server + "_" + name
	var b strings.Builder
	for _, r := range strings.ToLower(raw) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func parseToolDuration(raw string, fallback time.Duration) time.Duration {
	if value, err := time.ParseDuration(raw); err == nil && value > 0 {
		return value
	}
	return fallback
}
