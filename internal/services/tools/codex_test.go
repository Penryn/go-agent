package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	base := NewCodexTool(config.CodexConfig{Enabled: true, Binary: fake, CWD: dir, Timeout: "2s"})
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

func TestMCPToolNameIsNamespaced(t *testing.T) {
	if got := mcpToolName("Web Search", "news/latest"); got != "mcp_web_search_news_latest" {
		t.Fatalf("unexpected MCP tool name %q", got)
	}
}
