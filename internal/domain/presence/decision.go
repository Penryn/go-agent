package presence

import (
	"time"

	relationshipdomain "github.com/phlin/go-agent/internal/domain/relationship"
	scenedomain "github.com/phlin/go-agent/internal/domain/scene"
)

// ParticipationDecision 社交决策，分离"是否参与"和"如何回复"。
type ParticipationDecision struct {
	DecisionID     string    `json:"decision_id"`
	GroupID        int64     `json:"group_id"`
	TriggerEventID string    `json:"trigger_event_id"`
	DecidedAt      time.Time `json:"decided_at"`

	// 决策结果
	Participate bool   `json:"participate"`
	ReasonCode  string `json:"reason_code"` // 硬规则代码或模型判断原因

	// 参与目标
	TargetUserID int64  `json:"target_user_id,omitempty"` // 主要对话对象
	Audience     string `json:"audience"`                 // "target_only" / "group" / "public"

	// 社交意图
	Intent string `json:"intent"` // "respond" / "continue" / "initiate" / "moderate" / "observe"

	// 决策置信度和风险
	SocialValue      float64 `json:"social_value"`      // 预期社交价值：-1.0 到 1.0
	InterruptionRisk float64 `json:"interruption_risk"` // 打断风险：0.0-1.0
	ConfidenceScore  float64 `json:"confidence_score"`  // 决策置信度：0.0-1.0

	// 过期时间
	ExpiresAt time.Time `json:"expires_at"`

	// 决策依据（可选，用于审计）
	RuleHits             []string                  `json:"rule_hits,omitempty"` // 命中的硬规则
	SceneSnapshot        *scenedomain.GroupScene   `json:"scene_snapshot,omitempty"`
	RelationshipSnapshot *relationshipdomain.State `json:"relationship_snapshot,omitempty"`
}

// DecisionReasonCode 决策原因代码
const (
	// 硬规则拒绝
	ReasonBlacklisted      = "blacklisted"
	ReasonCooldown         = "cooldown"
	ReasonConsecutiveLimit = "consecutive_limit"
	ReasonEventExpired     = "event_expired"
	ReasonPermissionDenied = "permission_denied"
	ReasonSelfMessage      = "self_message"

	// 场景判断拒绝
	ReasonNoResponse       = "no_response_needed"
	ReasonFastConversation = "fast_conversation"
	ReasonTopicClosed      = "topic_closed"

	// 关系判断拒绝
	ReasonLowFamiliarity = "low_familiarity"
	ReasonHighFriction   = "high_friction"
	ReasonLowTrust       = "low_trust"

	// 人格判断拒绝
	ReasonLowEnergy        = "low_energy"
	ReasonLowPatience      = "low_patience"
	ReasonPostureWithdrawn = "posture_withdrawn"

	// 模型判断拒绝
	ReasonModelSilent   = "model_silent"
	ReasonLowConfidence = "low_confidence"

	// 参与原因
	ReasonDirectMention    = "direct_mention"
	ReasonQuestionToBot    = "question_to_bot"
	ReasonTopicMatch       = "topic_match"
	ReasonRelationshipGood = "relationship_good"
	ReasonOpportuneMoment  = "opportune_moment"
	ReasonModelInitiate    = "model_initiate"
)

// ResponsePlan 结构化回复计划，替代当前的工具编排。
type ResponsePlan struct {
	PlanID       string    `json:"plan_id"`
	DecisionID   string    `json:"decision_id"`
	GroupID      int64     `json:"group_id"`
	TargetUserID int64     `json:"target_user_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`

	// 主要动作（必选其一）
	PrimaryAction ActionPlan `json:"primary_action"`

	// 可选的次要动作（如先回应表情再回复文本）
	SecondaryActions []ActionPlan `json:"secondary_actions,omitempty"`

	// 社交目的
	SocialGoal string `json:"social_goal"` // "build_rapport" / "provide_help" / "lighten_mood" / "clarify" / "moderate"

	// 预期效果
	ExpectedOutcome string `json:"expected_outcome,omitempty"`

	// 风险评估
	RiskLevel string `json:"risk_level"` // "low" / "medium" / "high"
	RiskNote  string `json:"risk_note,omitempty"`
}

// ActionPlan 单个动作计划
type ActionPlan struct {
	ActionType string `json:"action_type"` // "speak" / "quote" / "meme" / "react" / "poke" / "silent"

	// 动作参数（按类型使用）
	Text        string `json:"text,omitempty"`          // speak / quote
	QuoteID     string `json:"quote_id,omitempty"`      // quote
	MemeAssetID string `json:"meme_asset_id,omitempty"` // meme
	Emoji       string `json:"emoji,omitempty"`         // react
	PokeUserID  int64  `json:"poke_user_id,omitempty"`  // poke

	// 动作意图说明
	Intent string `json:"intent,omitempty"`

	// 执行顺序（用于 SecondaryActions）
	Sequence int `json:"sequence,omitempty"`
}

// FeedbackWindow 发送后的反馈观察窗口
type FeedbackWindow struct {
	WindowID          string    `json:"window_id"`
	DecisionID        string    `json:"decision_id"`
	ActionID          string    `json:"action_id"`           // 已发送动作的内部 ID
	PlatformMessageID string    `json:"platform_message_id"` // 平台实际消息 ID
	GroupID           int64     `json:"group_id"`
	SentAt            time.Time `json:"sent_at"`

	// 观察配置
	ObserveDuration time.Duration `json:"observe_duration"` // 观察时长
	MaxEvents       int           `json:"max_events"`       // 最多观察事件数

	// 窗口状态
	Status   string    `json:"status"` // "observing" / "completed" / "cancelled"
	ClosedAt time.Time `json:"closed_at,omitempty"`

	// 收集到的事件
	ObservedEventIDs []string `json:"observed_event_ids"`

	// 反馈分类结果
	FeedbackType string `json:"feedback_type,omitempty"` // "positive" / "negative" / "neutral" / "ignored"
	FeedbackNote string `json:"feedback_note,omitempty"`
}

// FeedbackType 反馈类型常量
const (
	FeedbackPositive = "positive" // 被继续回复、被引用、被认可、被追问
	FeedbackNegative = "negative" // 被纠正、被要求停止、引发冲突
	FeedbackNeutral  = "neutral"  // 有回应但未明确正负
	FeedbackIgnored  = "ignored"  // 无人接话、话题转移
)
