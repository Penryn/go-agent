package tools

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// gatedTool 包装一个工具以控制其可用性
// 当工具不被允许时，调用会返回错误而不是执行
type gatedTool struct {
	tool tool.InvokableTool
}

func (t *gatedTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.tool.Info(ctx)
}

func (t *gatedTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	return "", fmt.Errorf("tool %q is not allowed in this group", t.toolName())
}

func (t *gatedTool) toolName() string {
	info, err := t.tool.Info(context.Background())
	if err == nil && info != nil {
		return info.Name
	}
	return "unknown"
}

// registeredTool 表示已注册的工具（内置或外部）
type registeredTool struct {
	name     string
	tool     tool.BaseTool
	external bool
	// terminal 工具终结 agent 循环，产出本轮对外的 ReplyPlan
	terminal bool
}

// namedTool 是内部工具的通用接口
// 所有内置工具都实现此接口
type namedTool interface {
	tool.InvokableTool
	Name() string
}
