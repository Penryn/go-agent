package policy

import "time"

type AutonomyState string

const (
	StateObserving AutonomyState = "observing"
	StateCooldown  AutonomyState = "cooldown"
)

type DecisionAction string

const (
	ActionSilent    DecisionAction = "silent"
	ActionReply     DecisionAction = "reply"
	ActionReact     DecisionAction = "react"
	ActionMemeOnly  DecisionAction = "meme_only"
	ActionPokeBack  DecisionAction = "poke_back"
	ActionPokeReply DecisionAction = "poke_reply" // 被戳后以对话方式回复
	ActionRecall    DecisionAction = "recall"
	ActionRepair    DecisionAction = "repair"
)

type GroupPolicy struct {
	GroupID           int64          `json:"group_id" yaml:"group_id"`
	PresenceLevel     string         `json:"presence_level" yaml:"presence_level"`
	PersonaOverlay    map[string]any `json:"persona_overlay" yaml:"persona_overlay"`
	ToolAllowlist     []string       `json:"tool_allowlist" yaml:"tool_allowlist"`
	MaxConsecutiveBot int            `json:"max_consecutive_bot" yaml:"max_consecutive_bot"`
}

type AutonomyPolicy struct {
	ObserveWindowSize        int     `json:"observe_window_size" yaml:"observe_window_size"`
	MinReplyIntervalSec      int     `json:"min_reply_interval_sec" yaml:"min_reply_interval_sec"`
	ProactiveBaseProbability float64 `json:"proactive_base_probability" yaml:"proactive_base_probability"`
	ProactiveScoreThreshold  float64 `json:"proactive_score_threshold" yaml:"proactive_score_threshold"`
}

type RuntimeState struct {
	GroupID             int64         `json:"group_id" yaml:"group_id"`
	State               AutonomyState `json:"state" yaml:"state"`
	CooldownUntil       time.Time     `json:"cooldown_until" yaml:"cooldown_until"`
	SuppressedUntil     time.Time     `json:"suppressed_until" yaml:"suppressed_until"`
	LastBotSpeakAt      time.Time     `json:"last_bot_speak_at" yaml:"last_bot_speak_at"`
	LastDirectedAt      time.Time     `json:"last_directed_at" yaml:"last_directed_at"`
	LastProactiveAt     time.Time     `json:"last_proactive_at" yaml:"last_proactive_at"`
	ConsecutiveBotTurns int           `json:"consecutive_bot_turns" yaml:"consecutive_bot_turns"`
	RepliesLast10Min    int           `json:"replies_last_10min" yaml:"replies_last_10min"`
	CurrentMood         string        `json:"current_mood" yaml:"current_mood"`
	CurrentEnergy       string        `json:"current_energy" yaml:"current_energy"`
	CurrentTopic        string        `json:"current_topic" yaml:"current_topic"`
}

type AutonomyDecision struct {
	DecisionID  string         `json:"decision_id" yaml:"decision_id"`
	Action      DecisionAction `json:"action" yaml:"action"`
	TriggerType string         `json:"trigger_type" yaml:"trigger_type"`
	Score       float64        `json:"score" yaml:"score"`
	Confidence  float64        `json:"confidence" yaml:"confidence"`
	ReasonCodes []string       `json:"reason_codes" yaml:"reason_codes"`
}
