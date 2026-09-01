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
