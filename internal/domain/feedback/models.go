// Package feedback 定义发送后互动反馈的领域模型
package feedback

import "time"

// ActionFeedback 表示一次发送动作后收集到的反馈。
type ActionFeedback struct {
	FeedbackID string    `json:"feedback_id"`
	ActionID   string    `json:"action_id"` // 对应的 action ID
	DecisionID string    `json:"decision_id"`
	GroupID    int64     `json:"group_id"`
	CollectedAt time.Time `json:"collected_at"`

	// 观察窗口内的事件
	ObservedEventIDs []string `json:"observed_event_ids"`

	// 反馈分类
	Type FeedbackType `json:"type"`

	// 详细信号
	Signals []FeedbackSignal `json:"signals"`

	// 综合评估
	OverallSentiment float64 `json:"overall_sentiment"` // -1.0 到 1.0
	EngagementLevel  float64 `json:"engagement_level"`  // 0.0-1.0

	// 关键证据
	KeyEvidenceEventID string `json:"key_evidence_event_id,omitempty"`
	SummaryNote        string `json:"summary_note,omitempty"`
}

// FeedbackType 反馈类型
type FeedbackType string

const (
	TypePositive FeedbackType = "positive"
	TypeNegative FeedbackType = "negative"
	TypeNeutral  FeedbackType = "neutral"
	TypeIgnored  FeedbackType = "ignored"
)

// FeedbackSignal 具体的反馈信号
type FeedbackSignal struct {
	SignalType string  `json:"signal_type"`
	Intensity  float64 `json:"intensity"`  // 0.0-1.0
	EventID    string  `json:"event_id,omitempty"`
	UserID     int64   `json:"user_id,omitempty"`
	Note       string  `json:"note,omitempty"`
}

// 反馈信号类型常量
const (
	// 正向信号
	SignalContinued      = "continued"       // 被继续回复
	SignalQuoted         = "quoted"          // 被引用
	SignalAgreed         = "agreed"          // 被认可
	SignalAskedMore      = "asked_more"      // 被追问
	SignalThanked        = "thanked"         // 被感谢
	SignalLaughed        = "laughed"         // 引发笑声
	SignalShared         = "shared"          // 被分享/转发

	// 负向信号
	SignalCorrected      = "corrected"       // 被纠正
	SignalRejected       = "rejected"        // 被拒绝
	SignalAskedToStop    = "asked_to_stop"   // 被要求停止
	SignalIgnoredHard    = "ignored_hard"    // 明确无视
	SignalTriggeredConflict = "triggered_conflict" // 引发冲突
	SignalComplained     = "complained"      // 被投诉

	// 中性信号
	SignalTopicShifted   = "topic_shifted"   // 话题转移
	SignalBriefResponse  = "brief_response"  // 简短回应
	SignalNoResponse     = "no_response"     // 无回应
	SignalOthersContinue = "others_continue" // 其他人继续对话
)

// FeedbackClassifier 反馈分类器接口（由 application 层实现）
type FeedbackClassifier interface {
	// ClassifyFeedback 分析观察窗口内的事件，生成反馈分类
	ClassifyFeedback(actionID string, observedEventIDs []string) (*ActionFeedback, error)
}
