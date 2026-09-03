package tools

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type testMCPTool struct{}

func (testMCPTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "mcp_demo_search", Desc: "search demo"}, nil
}

func TestMCPManagerToolInfos(t *testing.T) {
	manager := NewMCPManager(nil, &MCPTools{Tools: []tool.BaseTool{testMCPTool{}}}, nil)
	infos, err := manager.ToolInfos(context.Background())
	if err != nil || len(infos) != 1 || infos[0].Name != "mcp_demo_search" || infos[0].Description != "search demo" {
		t.Fatalf("unexpected tool infos: %+v, %v", infos, err)
	}
}
