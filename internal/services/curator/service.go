package curator

import (
	"fmt"
	"strings"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	memsvc "github.com/phlin/go-agent/internal/services/memory"
)

// Extract 从一轮对话快照里提取群聊亮点。confidence=0.8
// 恒大于 review 的过滤线 0.7，所以整条链等价于「短文本直接入库」。
func Extract(snapshot conversationdomain.ContextSnapshot) []memsvc.WriteIntent {
	text := strings.TrimSpace(snapshot.Event.Text)
	if text == "" || len([]rune(text)) > 24 {
		return nil
	}
	return []memsvc.WriteIntent{{
		Scope:         fmt.Sprintf("group:%d", snapshot.Event.GroupID),
		MemoryType:    "conversation_highlight",
		Subject:       "event",
		Content:       text,
		SourceEventID: snapshot.Event.EventID,
		Importance:    0.7,
		Confidence:    0.8,
	}}
}
