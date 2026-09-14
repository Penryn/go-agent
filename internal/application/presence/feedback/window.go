// Package feedback 处理发送后的反馈窗口和反馈分类
package feedback

import (
	"context"
	"strings"
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
	relationshipdomain "github.com/phlin/go-agent/internal/domain/relationship"
)

const (
	// FeedbackWindowDuration 反馈窗口持续时间
	FeedbackWindowDuration = 30 * time.Second
	// MaxFeedbackWindows 最多保留的反馈窗口数量
	MaxFeedbackWindows = 3
)

// SentimentAnalyzer 情绪分析接口
type SentimentAnalyzer interface {
	AnalyzeSentiment(ctx context.Context, messages []string) (float64, error)
}

// WindowManager 管理反馈窗口的生命周期
type WindowManager struct {
	relationshipRecorder RelationshipRecorder
	sentimentAnalyzer    SentimentAnalyzer // 可选：使用 LLM 进行情绪分析
}

// RelationshipRecorder 用于记录关系事件
type RelationshipRecorder interface {
	Apply(ctx context.Context, event relationshipdomain.Event) error
}

func NewWindowManager(recorder RelationshipRecorder) *WindowManager {
	return &WindowManager{
		relationshipRecorder: recorder,
		sentimentAnalyzer:    nil, // 默认使用基于规则的分类
	}
}

// WithSentimentAnalyzer 设置情绪分析器（可选）
func (m *WindowManager) WithSentimentAnalyzer(analyzer SentimentAnalyzer) *WindowManager {
	m.sentimentAnalyzer = analyzer
	return m
}

// OpenWindow 为刚发送的消息创建反馈窗口
func (m *WindowManager) OpenWindow(memory *presencedomain.GroupWorkingMemory, actionID, platformMessageID, decisionID string) {
	now := time.Now()
	window := presencedomain.FeedbackWindow{
		WindowID:          actionID + "-feedback",
		DecisionID:        decisionID,
		ActionID:          actionID,
		PlatformMessageID: platformMessageID,
		GroupID:           memory.GroupID,
		SentAt:            now,
		ObserveDuration:   FeedbackWindowDuration,
		MaxEvents:         10,
		Status:            "observing",
		ObservedEventIDs:  []string{},
	}

	// 添加新窗口并清理过期的
	memory.FeedbackWindows = append(memory.FeedbackWindows, window)
	m.pruneWindows(memory, now)
}

// CheckInboundEvent 检查入站事件是否属于某个反馈窗口
func (m *WindowManager) CheckInboundEvent(ctx context.Context, memory *presencedomain.GroupWorkingMemory, event conversationdomain.ConversationEvent) error {
	now := time.Now()

	// 遍历所有未关闭的反馈窗口
	for i := range memory.FeedbackWindows {
		window := &memory.FeedbackWindows[i]

		if window.Status != "observing" {
			continue
		}

		// 检查是否在窗口时间内
		windowEnd := window.SentAt.Add(window.ObserveDuration)
		if now.After(windowEnd) {
			// 窗口已过期，分析并关闭
			if err := m.closeWindow(ctx, memory, window); err != nil {
				return err
			}
			continue
		}

		// 检查是否是对机器人消息的回复或相关事件
		if m.isResponseToBotMessage(event, window.PlatformMessageID, window.SentAt) {
			window.ObservedEventIDs = append(window.ObservedEventIDs, event.EventID)

			// 达到最大事件数，提前关闭
			if len(window.ObservedEventIDs) >= window.MaxEvents {
				if err := m.closeWindow(ctx, memory, window); err != nil {
					return err
				}
			}
		}
	}

	// 清理已关闭的窗口
	m.pruneWindows(memory, now)
	return nil
}

// isResponseToBotMessage 判断事件是否是对机器人消息的回复
func (m *WindowManager) isResponseToBotMessage(event conversationdomain.ConversationEvent, botMessageID string, sentAt time.Time) bool {
	if event.Origin == string(presencedomain.OriginOutbound) || event.EventID == "" {
		return false
	}
	// 只接受有明确关联证据的消息。单纯“刚好在30秒内发言”不再算作反馈。
	return event.ReplyToMessageID == botMessageID || event.IsReplyToBot || event.MentionedBot || event.NamedBot
}

// closeWindow 关闭反馈窗口并分析反馈类型
func (m *WindowManager) closeWindow(ctx context.Context, memory *presencedomain.GroupWorkingMemory, window *presencedomain.FeedbackWindow) error {
	window.Status = "completed"
	window.ClosedAt = time.Now()

	if len(window.ObservedEventIDs) == 0 {
		// 没有反馈 = 被忽略
		window.FeedbackType = presencedomain.FeedbackIgnored
		window.FeedbackNote = "No response within observation window"
		return nil
	}

	// 分析反馈情绪
	sentiment, firstUserID := m.analyzeSentiment(ctx, memory, window.ObservedEventIDs)

	if sentiment > 0.3 {
		// 正面反馈
		window.FeedbackType = presencedomain.FeedbackPositive
		window.FeedbackNote = "Positive sentiment detected"
		return m.recordFeedback(ctx, memory.GroupID, window, relationshipdomain.EventPositiveFeedback, sentiment, firstUserID)
	} else if sentiment < -0.3 {
		// 负面反馈
		window.FeedbackType = presencedomain.FeedbackNegative
		window.FeedbackNote = "Negative sentiment detected"
		return m.recordFeedback(ctx, memory.GroupID, window, relationshipdomain.EventNegativeFeedback, sentiment, firstUserID)
	} else {
		// 中性 = 对话继续
		window.FeedbackType = presencedomain.FeedbackNeutral
		window.FeedbackNote = "Neutral continuation"
		return m.recordFeedback(ctx, memory.GroupID, window, relationshipdomain.EventConversationKept, sentiment, firstUserID)
	}
}

// analyzeSentiment 分析回复的情绪倾向
func (m *WindowManager) analyzeSentiment(ctx context.Context, memory *presencedomain.GroupWorkingMemory, responseIDs []string) (float64, int64) {
	if len(responseIDs) == 0 {
		return 0, 0
	}

	var messages []string
	var firstUserID int64

	// 从 RecentTail 中找到对应的事件并提取消息
	for _, eventID := range responseIDs {
		for _, record := range memory.RecentTail {
			if record.EventID == eventID {
				if firstUserID == 0 {
					firstUserID = record.UserID
				}
				messages = append(messages, record.Event.Text)
				break
			}
		}
	}

	if len(messages) == 0 {
		return 0, firstUserID
	}

	// 如果配置了 LLM 情绪分析器，优先使用
	if m.sentimentAnalyzer != nil {
		sentiment, err := m.sentimentAnalyzer.AnalyzeSentiment(ctx, messages)
		if err == nil {
			return sentiment, firstUserID
		}
		// LLM 失败时降级到规则分类
	}

	// 降级：使用基于规则的情绪分类
	totalSentiment := 0.0
	for _, text := range messages {
		totalSentiment += classifyMessageSentiment(text)
	}
	return totalSentiment / float64(len(messages)), firstUserID
}

// classifyMessageSentiment 简单的情绪分类
func classifyMessageSentiment(text string) float64 {
	text = strings.ToLower(text)

	// 正面词汇
	positiveWords := []string{
		"谢谢", "感谢", "好的", "可以", "赞", "👍", "🙏", "❤️",
		"哈哈", "😂", "😄", "😊", "棒", "厉害", "牛", "对", "是的",
		"不错", "喜欢", "爱了", "懂了", "明白", "学到了",
	}

	// 负面词汇
	negativeWords := []string{
		"错", "不对", "别", "不要", "烦", "闭嘴", "傻", "蠢",
		"😡", "🙄", "无语", "算了", "拜拜", "滚", "垃圾",
		"废话", "有病", "神经", "脑子",
	}

	score := 0.0
	for _, word := range positiveWords {
		if strings.Contains(text, word) {
			score += 0.3
		}
	}
	for _, word := range negativeWords {
		if strings.Contains(text, word) {
			score -= 0.3
		}
	}

	// 限制在 [-1, 1] 范围
	if score > 1 {
		score = 1
	} else if score < -1 {
		score = -1
	}

	return score
}

// recordFeedback 记录反馈事件到关系系统
func (m *WindowManager) recordFeedback(ctx context.Context, groupID int64, window *presencedomain.FeedbackWindow, kind relationshipdomain.EventKind, valence float64, userID int64) error {
	if m.relationshipRecorder == nil || userID == 0 {
		return nil
	}

	// TODO: 需要从上下文获取 PersonaID
	event := relationshipdomain.Event{
		EventID:         window.WindowID,
		PersonaID:       "", // 从配置获取
		GroupID:         groupID,
		UserID:          userID,
		Kind:            kind,
		Valence:         valence,
		EvidenceEventID: window.ActionID,
		DecisionID:      window.DecisionID,
		CreatedAt:       time.Now(),
	}

	return m.relationshipRecorder.Apply(ctx, event)
}

// pruneWindows 清理已关闭或过期的窗口，只保留最近的几个
func (m *WindowManager) pruneWindows(memory *presencedomain.GroupWorkingMemory, now time.Time) {
	// 清理已关闭且过期的窗口
	kept := make([]presencedomain.FeedbackWindow, 0, len(memory.FeedbackWindows))
	for _, window := range memory.FeedbackWindows {
		// 保留未关闭的或最近关闭的（5分钟内）
		if window.Status == "observing" || now.Sub(window.SentAt) < 5*time.Minute {
			kept = append(kept, window)
		}
	}

	// 只保留最近的 MaxFeedbackWindows 个
	if len(kept) > MaxFeedbackWindows {
		kept = kept[len(kept)-MaxFeedbackWindows:]
	}

	memory.FeedbackWindows = kept
}
