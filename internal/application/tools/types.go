package tools

import (
	"github.com/cloudwego/eino/components/tool"
)

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
