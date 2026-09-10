package feedback

import (
	"strings"
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
)

// RelevanceFilter 过滤明确相关的反馈消息
type RelevanceFilter struct {
	botUserID    int64
	botMessageID string
}

// NewRelevanceFilter 创建反馈相关性过滤器
func NewRelevanceFilter(botUserID int64, botMessageID string) *RelevanceFilter {
	return &RelevanceFilter{
		botUserID:    botUserID,
		botMessageID: botMessageID,
	}
}

// FilterExplicitFeedback 过滤出明确的反馈消息
// 只保留：直接回复、@提及、包含明确信号的消息
func (f *RelevanceFilter) FilterExplicitFeedback(
	events []conversationdomain.ConversationEvent,
) []conversationdomain.ConversationEvent {
	var relevant []conversationdomain.ConversationEvent

	for _, event := range events {
		if f.isExplicitFeedback(event) {
			relevant = append(relevant, event)
		}
	}

	return relevant
}

// isExplicitFeedback 判断是否是明确的反馈
func (f *RelevanceFilter) isExplicitFeedback(event conversationdomain.ConversationEvent) bool {
	// 1. 直接回复机器人消息
	if event.ReplyToMessageID == f.botMessageID {
		return true
	}

	// 2. @提及机器人或直接回复机器人
	if event.MentionedBot || event.IsReplyToBot {
		return true
	}

	// 3. 包含明确的纠正/赞同/拒绝信号
	if containsExplicitSignal(event.Text) {
		return true
	}

	return false
}

// containsExplicitSignal 检查是否包含明确的反馈信号
func containsExplicitSignal(text string) bool {
	text = strings.ToLower(text)

	// 纠正信号
	corrections := []string{
		"不对", "错了", "不是", "应该是", "其实是",
		"不对吧", "错误", "搞错了", "弄错了",
	}
	for _, signal := range corrections {
		if strings.Contains(text, signal) {
			return true
		}
	}

	// 明确的赞同信号（需要避免误判）
	agreements := []string{
		"没错", "对的", "正确", "就是这样",
		"说得对", "说得好", "赞同",
	}
	for _, signal := range agreements {
		if strings.Contains(text, signal) {
			return true
		}
	}

	// 明确的拒绝信号
	rejections := []string{
		"别说了", "闭嘴", "够了", "停下",
		"不要说", "别再说", "烦死了",
	}
	for _, signal := range rejections {
		if strings.Contains(text, signal) {
			return true
		}
	}

	return false
}

// FeedbackType 反馈类型
type FeedbackType string

const (
	TypeCorrection FeedbackType = "correction" // 纠正
	TypeAgreement  FeedbackType = "agreement"  // 赞同
	TypeRejection  FeedbackType = "rejection"  // 拒绝
	TypeEngagement FeedbackType = "engagement" // 继续互动
	TypeUnknown    FeedbackType = "unknown"    // 未知
)

// SimplifiedFeedback 简化的反馈结构
type SimplifiedFeedback struct {
	Type        FeedbackType `json:"type"`
	Confidence  float64      `json:"confidence"`   // 0-1
	KeyMessages []string     `json:"key_messages"` // 关键消息文本
	Reasoning   string       `json:"reasoning"`    // 判断理由
	CollectedAt time.Time    `json:"collected_at"`
}
