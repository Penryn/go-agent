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

func (c *Composer) Instruction(snapshot conversationdomain.ContextSnapshot, decision policydomain.AutonomyDecision) string {
	if decision.Action == policydomain.ActionSilent {
		sections := []string{
			"长期人格层:",
			fmt.Sprintf("你是 %s。%s。", c.persona.Name, c.persona.Description),
		}
		if c.persona.Background.Summary != "" {
			sections = append(sections, c.persona.Background.Summary)
		}
		sections = append(sections,
			"",
			"当前回合任务层:",
			"你现在处于观察模式，本轮不需要发言。",
			"可以使用 query_memory、mark_memory_intent、query_member_profile 等工具被动观察和记录对话内容。",
			"完成观察后必须用 stay_silent 结束。",
		)
		return strings.Join(sections, "\n")
	}

	// ── 长期人格层 ──────────────────────────────────────────────────────────
	sections := []string{
		"长期人格层:",
		fmt.Sprintf("你是 %s。%s。说话风格: %s。", c.persona.Name, c.persona.Description, c.persona.SpeechStyle),
	}
	if len(c.persona.Interests) > 0 {
		sections = append(sections, "感兴趣的话题: "+strings.Join(c.persona.Interests, "、")+"。")
	}
	if c.persona.Background.Summary != "" {
		sections = append(sections, c.persona.Background.Summary)
	}
	if len(c.persona.Traits) > 0 {
		sections = append(sections, "性格特征: "+strings.Join(c.persona.Traits, "，")+"。")
	}
	for _, hint := range c.persona.Background.BehaviorHints {
		if hint != "" {
			sections = append(sections, hint)
		}
	}

	// ── 人格状态层 ──────────────────────────────────────────────────────────
	mood := defaultMood(snapshot.PersonaState.Mood)
	energy := defaultEnergy(snapshot.PersonaState.Energy)
	sections = append(sections,
		"",
		"人格状态层:",
		fmt.Sprintf("当前心情=%s，精力=%s。", mood, energy),
		"请让回复风格自然地反映当前心情和精力水平。心情和精力同样影响你是否愿意配合对方的行为请求。",
	)
	if hint := requestDispositionHint(mood, energy); hint != "" {
		sections = append(sections, hint)
	}

	// ── 关系状态层 ──────────────────────────────────────────────────────────
	sections = append(sections,
		"",
		"关系状态层:",
		fmt.Sprintf("你与该用户的熟悉度=%.2f，好感度=%.2f。", snapshot.RelationshipState.Familiarity, snapshot.RelationshipState.Affinity),
		"熟悉度和好感度越高，可以越随意和亲近；反之保持礼貌距离。",
	)

	// ── 说话风格层（Speech 全空时跳过整层）──────────────────────────────────
	sp := c.persona.Speech
	hasSpeechSection := len(sp.Catchphrases) > 0 ||
		len(sp.Avoidances) > 0 ||
		sp.EmojiFrequency != "" ||
		len(sp.FewShotExamples) > 0
	if hasSpeechSection {
		sections = append(sections, "", "说话风格层:")
		if len(sp.Catchphrases) > 0 {
			sections = append(sections, "偶尔习惯用语: "+strings.Join(sp.Catchphrases, "、")+"。")
		}
		if len(sp.Avoidances) > 0 {
			sections = append(sections, "不要说: "+strings.Join(sp.Avoidances, "、")+"。")
		}
		if sp.EmojiFrequency != "" {
			sections = append(sections, "表情包使用频率: "+sp.EmojiFrequency+"。")
		}
		if len(sp.FewShotExamples) > 0 {
			sections = append(sections, "参考示例:")
			for _, ex := range sp.FewShotExamples {
				sections = append(sections, fmt.Sprintf("用户: %s\n你: %s", ex.UserSays, ex.BotSays))
			}
		}
	}

	// ── 当前群策略层 ────────────────────────────────────────────────────────
	sections = append(sections,
		"",
		"当前群策略层:",
		fmt.Sprintf("本群存在感=%s。允许工具=%s。", snapshot.GroupPolicy.PresenceLevel, strings.Join(snapshot.GroupPolicy.ToolAllowlist, ",")),
		"夜间和高频场景要更克制。",
	)

	// ── 当前回合任务层 ──────────────────────────────────────────────────────
	taskLines := []string{
		"",
		"当前回合任务层:",
		"你只负责在已经决定可以说话时，选择自然的回复方式。",
		"如果需要收集信息，可以先用 query_memory 或 search_meme；最终必须用 speak_text、quote_reply 或 stay_silent 结束。",
		"若消息上下文中提供了 msg_id 且用户明确要求引用特定消息，优先使用 quote_reply 并传入对应 msg_id。",
		"同一用户在极短时间内连续发送的多条消息通常是一个完整意思的分条发送，必须把它们合并为一个整体语义单元理解，不得孤立解读最后一条。",
		"遇到不认识的词汇、专有名词、群内黑话、缩写或近期事件时，禁止猜测含义直接回复；必须先调用 web_search 查证，若搜索结果仍无法确认，坦承不了解并向对方发问，不得用模糊措辞硬撑。",
		"收到「帮我做XX」「帮我查XX」「陪我XX」「来一起XX」等行为请求时，不默认服从；结合上方心情倾向和关系好感度自主判断是否配合。好感度偏低（冷淡区间）或心情差时，倾向拒绝或敷衍；好感度高且心情好时，可以适当配合。",
	}
	if decision.TriggerType == "poke_reply" {
		taskLines = append(taskLines,
			"触发事件：对方戳了你一下（无文字内容）。请根据当前心情和与对方的关系自然回应这个小互动，可以是调侃、疑问、搭腔、或简短敷衍。不要说「你戳我做什么」这类机械问句，也不要解释「你戳了我一下」，直接自然发挥。",
		)
	}
	sections = append(sections, taskLines...)

	// ── 输出约束层 ──────────────────────────────────────────────────────────
	sections = append(sections,
		"",
		"输出约束层:",
		fmt.Sprintf("最多 %d 字，最多 %d 句。", c.persona.ReplyMaxChars, c.persona.ReplyMaxSentences),
		"避免客服腔、总结腔、长解释。",
		"拒绝行为请求时禁止说「很抱歉」「抱歉无法帮您」「我无法完成」等客服式措辞；用角色自然语气拒绝，例如「懒得动」「你不会自己查吗」「没空」「不想」。",
	)
	for _, constraint := range c.persona.Constraints {
		if constraint != "" {
			sections = append(sections, constraint)
		}
	}
	if c.persona.AllowTeasing {
		sections = append(sections, "可以适度调侃。")
	}
	if c.persona.AllowQuestions {
		sections = append(sections, "可以向对方反问。")
	}

	return strings.Join(sections, "\n")
}

func defaultMood(mood string) string {
	if mood == "" {
		return "steady"
	}
	return mood
}

func defaultEnergy(energy string) string {
	if energy == "" {
		return "normal"
	}
	return energy
}

// requestDispositionHint 根据当前心情和精力返回对「是否配合行为请求」的倾向提示。
// steady 状态不做额外干预（由调用方结合好感度在关系状态层已有描述覆盖冷淡区间）。
// 返回空字符串时调用方不注入任何额外文本。
func requestDispositionHint(mood, energy string) string {
	switch mood {
	case "aggro", "irritated", "angry":
		return "当前状态倾向：可以直接敷衍或拒绝行为请求，不必客气。"
	case "withdrawn", "tired", "low":
		return "当前状态倾向：偏向少说少动，行为请求能推就推。"
	case "happy", "excited":
		if energy == "high" {
			return "当前状态倾向：心情好且精力充沛，可以适当配合感兴趣的请求。"
		}
		return ""
	default:
		// steady / normal / 其他未知值：不额外注入，由好感度描述自然覆盖
		return ""
	}
}

func (c *Composer) Messages(snapshot conversationdomain.ContextSnapshot) []*schema.Message {
	baseTS := snapshot.Event.TimestampUnix // 当前触发消息时间戳，作为相对时间基准
	currentUserID := snapshot.Event.UserID

	recentTurns := make([]string, 0, len(snapshot.RecentTurns))
	for _, turn := range snapshot.RecentTurns {
		if strings.TrimSpace(turn.Text) == "" {
			continue
		}

		// 角色标签：区分当前触发用户与其他群成员
		var roleTag string
		if turn.UserID == currentUserID {
			roleTag = "[当前用户]"
		} else {
			roleTag = fmt.Sprintf("[用户%d]", turn.UserID)
		}

		// 相对时间戳：仅在两端时间戳均有效时计算，防止零值导致异常
		var timeTag string
		if baseTS > 0 && turn.TimestampUnix > 0 {
			diffSec := baseTS - turn.TimestampUnix
			if diffSec < 0 {
				diffSec = 0 // 防御时钟漂移
			}
			timeTag = fmt.Sprintf("[T-%ds]", diffSec)
		}

		recentTurns = append(recentTurns, fmt.Sprintf("%s%s %s", timeTag, roleTag, strings.TrimSpace(turn.Text)))
	}

	memorySnippets := make([]string, 0, len(snapshot.RelevantMemories))
	for _, record := range snapshot.RelevantMemories {
		memorySnippets = append(memorySnippets, fmt.Sprintf("%s:%s", record.Subject, record.Content))
	}

	mediaSnippets := make([]string, 0, len(snapshot.MediaDescriptors))
	for _, descriptor := range snapshot.MediaDescriptors {
		// P1-1: 分层注入 Descriptor 结构化字段，仅注入非空字段，不注入 SafetySignals
		parts := []string{fmt.Sprintf("[%s]%s", descriptor.Kind, descriptor.Summary)}
		if len(descriptor.OCRTexts) > 0 {
			parts = append(parts, "OCR:"+strings.Join(descriptor.OCRTexts, "/"))
		}
		if len(descriptor.EmotionHints) > 0 {
			parts = append(parts, "情绪:"+strings.Join(descriptor.EmotionHints, "/"))
		}
		if len(descriptor.MemeSignals) > 0 {
			parts = append(parts, "梗:"+strings.Join(descriptor.MemeSignals, "/"))
		}
		if len(descriptor.MemeKeywords) > 0 {
			parts = append(parts, "梗词:"+strings.Join(descriptor.MemeKeywords, "/"))
		}
		if len(descriptor.SceneTags) > 0 {
			parts = append(parts, "场景:"+strings.Join(descriptor.SceneTags, "/"))
		}
		mediaSnippets = append(mediaSnippets, strings.Join(parts, " "))
	}

	workingState := make([]string, 0, 2)
	if topic := strings.TrimSpace(snapshot.ActiveTopic); topic != "" {
		workingState = append(workingState, "当前话题: "+topic)
	}
	if len(snapshot.OpenLoops) > 0 {
		workingState = append(workingState, "未解决问题: "+strings.Join(snapshot.OpenLoops, " / "))
	}

	content := strings.Join([]string{
		fmt.Sprintf("当前事件: user=%d msg_id=%s text=%q", snapshot.Event.UserID, snapshot.Event.MessageID, snapshot.Event.Text),
		fmt.Sprintf("最近上下文: %s", strings.Join(recentTurns, " | ")),
		fmt.Sprintf("工作记忆: %s", strings.Join(workingState, " | ")),
		fmt.Sprintf("相关记忆: %s", strings.Join(memorySnippets, " | ")),
		fmt.Sprintf("媒体摘要: %s", strings.Join(mediaSnippets, " | ")),
	}, "\n")

	return []*schema.Message{schema.UserMessage(content)}
}
