package tools

import (
	"slices"
)

// isTerminalTool 判断工具是否为终端工具
// 终端工具会结束 agent 循环并产生最终的 ReplyPlan
func isTerminalTool(name string) bool {
	switch name {
	case "speak_text", "stay_silent", "react_emoji", "send_meme", "quote_reply", "repair_message", "poke_member":
		return true
	}
	return false
}

// internalToolAllowed 检查内部工具是否在允许列表中
// 空列表表示允许所有工具
func internalToolAllowed(allowlist []string, name string) bool {
	return len(allowlist) == 0 || slices.Contains(allowlist, name)
}
