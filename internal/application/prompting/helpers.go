package prompting

import (
	"fmt"
	"strings"
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
)

// 格式化和辅助函数

var shanghaiLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

const (
	promptRecentTail       = 8
	promptCurrentUserTurns = 2
	promptBurstGapSeconds  = int64(1)
	promptBurstMaxSeconds  = int64(3)
)

// defaultMood 返回默认心情
func defaultMood(mood string) string {
	if mood == "" {
		return "steady"
	}
	return mood
}

// defaultEnergy 返回默认精力
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

// talkBiasHint 返回参与倾向提示
func talkBiasHint(bias float64) string {
	switch {
	case bias <= -0.2:
		return "当前参与倾向偏低：回复更克制，不主动扩展新话题。"
	case bias >= 0.2:
		return "当前参与倾向偏高：可以自然多接一句，但不要抢话。"
	default:
		return ""
	}
}

// sameEvent 判断两个事件是否相同
func sameEvent(left, right conversationdomain.ConversationEvent) bool {
	if right.EventID != "" && left.EventID == right.EventID {
		return true
	}
	return right.EventID == "" && right.MessageID != "" && left.MessageID == right.MessageID
}

// prepareDialogueTurns keeps the prompt focused without mutating the archived
// facts. It merges a rapid same-user split message into the current turn, then
// retains the conversational tail plus the current user's recent context and
// an explicitly replied-to message.
func prepareDialogueTurns(turns []conversationdomain.ConversationEvent, current conversationdomain.ConversationEvent, selfID int64) ([]conversationdomain.ConversationEvent, conversationdomain.ConversationEvent) {
	history := make([]conversationdomain.ConversationEvent, 0, len(turns))
	for _, turn := range turns {
		if sameEvent(turn, current) || strings.TrimSpace(turn.Text) == "" {
			continue
		}
		history = append(history, turn)
	}

	burstStart := len(history)
	if current.EventID != "" && current.TimestampUnix > 0 && current.UserID != 0 && current.UserID != selfID {
		lastTimestamp := current.TimestampUnix
		for i := len(history) - 1; i >= 0; i-- {
			turn := history[i]
			if turn.UserID != current.UserID || turn.UserID == selfID || turn.TimestampUnix <= 0 {
				break
			}
			if lastTimestamp-turn.TimestampUnix < 0 || lastTimestamp-turn.TimestampUnix > promptBurstGapSeconds || current.TimestampUnix-turn.TimestampUnix > promptBurstMaxSeconds {
				break
			}
			burstStart = i
			lastTimestamp = turn.TimestampUnix
		}
	}
	if burstStart < len(history) {
		parts := make([]string, 0, len(history)-burstStart+1)
		for _, turn := range history[burstStart:] {
			parts = append(parts, strings.TrimSpace(turn.Text))
		}
		parts = append(parts, strings.TrimSpace(current.Text))
		current.Text = strings.Join(parts, " ")
		history = history[:burstStart]
	}

	if len(history) <= promptRecentTail {
		return history, current
	}
	selected := make(map[int]struct{}, promptRecentTail+promptCurrentUserTurns+1)
	for i := len(history) - promptRecentTail; i < len(history); i++ {
		selected[i] = struct{}{}
	}
	currentUserTurns := 0
	for i := len(history) - 1; i >= 0 && currentUserTurns < promptCurrentUserTurns; i-- {
		if history[i].UserID == current.UserID {
			selected[i] = struct{}{}
			currentUserTurns++
		}
	}
	if current.ReplyToMessageID != "" {
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].MessageID == current.ReplyToMessageID {
				selected[i] = struct{}{}
				break
			}
		}
	}
	result := make([]conversationdomain.ConversationEvent, 0, len(selected))
	for i, turn := range history {
		if _, ok := selected[i]; ok {
			result = append(result, turn)
		}
	}
	return result, current
}

// stableHistoryTurn 格式化历史对话轮次
func stableHistoryTurn(turn conversationdomain.ConversationEvent, selfID int64) string {
	roleTag := fmt.Sprintf("[用户%d]", turn.UserID)
	if selfID != 0 && turn.UserID == selfID {
		roleTag = "[你]"
		if turn.MessageID != "" {
			roleTag += "[msg_id=" + turn.MessageID + "]"
		}
	} else {
		roleTag += senderIdentityTag(turn)
	}
	timeTag := "[时间未知]"
	if turn.TimestampUnix > 0 {
		timeTag = "[时间=" + time.Unix(turn.TimestampUnix, 0).In(shanghaiLocation).Format("2006-01-02 15:04:05") + "]"
	}
	return fmt.Sprintf("%s%s %s", timeTag, roleTag, strings.TrimSpace(turn.Text))
}

// formatMemorySnippet 格式化记忆片段
func formatMemorySnippet(record memorydomain.MemoryRecord) string {
	typeName := strings.TrimSpace(record.Type)
	if typeName == "" {
		typeName = "未分类"
	}
	created := "时间未知"
	if !record.CreatedAt.IsZero() {
		created = record.CreatedAt.In(shanghaiLocation).Format("2006-01-02")
	}
	source := strings.TrimSpace(record.Origin)
	if source == "" {
		source = "来源未知"
	}
	evidence := record.SourceEventID
	if len(record.SourceEventIDs) > 0 {
		evidence = strings.Join(record.SourceEventIDs, ",")
	}
	if evidence == "" {
		evidence = "证据未知"
	}
	return fmt.Sprintf("[%s][观察=%s][来源=%s][证据=%s] %s:%s", typeName, created, source, evidence, record.Subject, record.Content)
}

// addressSignal 返回寻址信号
func addressSignal(event conversationdomain.ConversationEvent) string {
	if event.MentionedBot || event.NamedBot || event.IsReplyToBot {
		return "收件人信号: 当前消息包含直接指向你的平台信号（@、别名点名或回复你的消息），可以按对你说的内容处理。"
	}
	return "收件人信号: 当前消息未检测到 @ 你、点名你的别名或回复你的消息；默认不是对你说的，优先 stay_silent。若话题确实有意思且你的补充有自然价值，可以作为群友插一句，但不要假装对方是在问你。"
}

// eventWithProfileIdentity 将用户资料身份信息合并到事件中
func eventWithProfileIdentity(event conversationdomain.ConversationEvent, profile profiledomain.MemberProfile) conversationdomain.ConversationEvent {
	if event.Sender.QQNickname == "" {
		event.Sender.QQNickname = profile.Stats.QQNickname
	}
	if event.Sender.GroupCard == "" {
		event.Sender.GroupCard = profile.Stats.GroupCard
	}
	if event.Sender.DisplayName == "" {
		event.Sender.DisplayName = event.Sender.GroupCard
		if event.Sender.DisplayName == "" {
			event.Sender.DisplayName = event.Sender.QQNickname
		}
		if event.Sender.DisplayName == "" {
			event.Sender.DisplayName = profile.Stats.Nickname
		}
	}
	return event
}

// senderIdentityTag 返回发送者身份标签
func senderIdentityTag(event conversationdomain.ConversationEvent) string {
	parts := make([]string, 0, 3)
	if value := promptData(event.Sender.GroupCard, 64); value != "" {
		parts = append(parts, "群昵称="+value)
	}
	if value := promptData(event.Sender.QQNickname, 64); value != "" {
		parts = append(parts, "QQ昵称="+value)
	}
	if event.UserID != 0 {
		parts = append(parts, fmt.Sprintf("QQ=%d", event.UserID))
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, "][") + "]"
}

// promptData 清理并截断数据以适应提示词长度限制
func promptData(value string, maxRunes int) string {
	value = strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ", "]", "］", "[", "［").Replace(value))
	runes := []rune(value)
	if maxRunes > 0 && len(runes) > maxRunes {
		value = string(runes[:maxRunes])
	}
	return value
}
