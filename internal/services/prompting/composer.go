package prompting

import (
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
)

type Composer struct {
	persona personadomain.PersonaConfig
}

func NewComposer(persona personadomain.PersonaConfig) *Composer {
	return &Composer{persona: persona}
}

func (c *Composer) Instruction(snapshot conversationdomain.ContextSnapshot, _ policydomain.AutonomyDecision) string {
	return strings.Join([]string{
		"长期人格层:",
		fmt.Sprintf("你是 %s。%s。说话风格: %s。", c.persona.Name, c.persona.Description, c.persona.SpeechStyle),
		"你像普通群友，不像客服，也不要说自己是 AI。",
		"",
		"当前群策略层:",
		fmt.Sprintf("本群存在感=%s。允许工具=%s。", snapshot.GroupPolicy.PresenceLevel, strings.Join(snapshot.GroupPolicy.ToolAllowlist, ",")),
		"夜间和高频场景要更克制。",
		"",
		"当前回合任务层:",
		"你只负责在已经决定可以说话时，选择自然的回复方式。",
		"如果需要收集信息，可以先用 query_memory 或 search_meme；最终必须用 speak_text 或 stay_silent 结束。",
		"",
		"输出约束层:",
		fmt.Sprintf("最多 %d 字，最多 %d 句。", c.persona.ReplyMaxChars, c.persona.ReplyMaxSentences),
		"避免客服腔、总结腔、长解释。",
	}, "\n")
}

func (c *Composer) Messages(snapshot conversationdomain.ContextSnapshot) []*schema.Message {
	recentTurns := make([]string, 0, len(snapshot.RecentTurns))
	for _, turn := range snapshot.RecentTurns {
		if strings.TrimSpace(turn.Text) == "" {
			continue
		}
		recentTurns = append(recentTurns, fmt.Sprintf("%d:%s", turn.UserID, strings.TrimSpace(turn.Text)))
	}

	memorySnippets := make([]string, 0, len(snapshot.RelevantMemories))
	for _, record := range snapshot.RelevantMemories {
		memorySnippets = append(memorySnippets, fmt.Sprintf("%s:%s", record.Subject, record.Content))
	}

	mediaSnippets := make([]string, 0, len(snapshot.MediaDescriptors))
	for _, descriptor := range snapshot.MediaDescriptors {
		mediaSnippets = append(mediaSnippets, descriptor.Summary)
	}

	content := strings.Join([]string{
		fmt.Sprintf("当前事件: user=%d text=%q", snapshot.Event.UserID, snapshot.Event.Text),
		fmt.Sprintf("最近上下文: %s", strings.Join(recentTurns, " | ")),
		fmt.Sprintf("相关记忆: %s", strings.Join(memorySnippets, " | ")),
		fmt.Sprintf("媒体摘要: %s", strings.Join(mediaSnippets, " | ")),
	}, "\n")

	return []*schema.Message{schema.UserMessage(content)}
}
