package reply

import (
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
)

type ReplyIntent struct {
	Kind              string  `json:"kind" yaml:"kind"`
	Goal              string  `json:"goal" yaml:"goal"`
	TargetUserIDs     []int64 `json:"target_user_ids" yaml:"target_user_ids"`
	NeedClarification bool    `json:"need_clarification" yaml:"need_clarification"`
	PreferMeme        bool    `json:"prefer_meme" yaml:"prefer_meme"`
	PreferShortText   bool    `json:"prefer_short_text" yaml:"prefer_short_text"`
	MaxChars          int     `json:"max_chars" yaml:"max_chars"`
}

type ReplyPlan struct {
	PlanID             string                        `json:"plan_id" yaml:"plan_id"`
	Intent             ReplyIntent                   `json:"intent" yaml:"intent"`
	ReplyToMessageID   string                        `json:"reply_to_message_id" yaml:"reply_to_message_id"`
	Bubbles            []string                      `json:"bubbles" yaml:"bubbles"`
	PlannedTools       []string                      `json:"planned_tools" yaml:"planned_tools"`
	PlannedActions     []policydomain.DecisionAction `json:"planned_actions" yaml:"planned_actions"`
	ActionParams       map[string]any                `json:"action_params,omitempty" yaml:"action_params,omitempty"`
	MemoryWriteIntents []string                      `json:"memory_write_intents" yaml:"memory_write_intents"`
	SendMode           string                        `json:"send_mode" yaml:"send_mode"`
	FallbackText       string                        `json:"fallback_text" yaml:"fallback_text"`
}

// ThoughtRecord is the durable, user-independent summary of one deliberation.
// It records the decision basis without persisting private chain-of-thought.
type ThoughtRecord struct {
	ThoughtID      string    `json:"thought_id" yaml:"thought_id"`
	CandidateID    string    `json:"candidate_id" yaml:"candidate_id"`
	GroupID        int64     `json:"group_id" yaml:"group_id"`
	EventID        string    `json:"event_id" yaml:"event_id"`
	Interpretation string    `json:"interpretation" yaml:"interpretation"`
	Evidence       []string  `json:"evidence" yaml:"evidence"`
	Uncertainty    float64   `json:"uncertainty" yaml:"uncertainty"`
	ChosenAction   string    `json:"chosen_action" yaml:"chosen_action"`
	Outcome        string    `json:"outcome" yaml:"outcome"`
	CreatedAt      time.Time `json:"created_at" yaml:"created_at"`
}

type ToolContext struct {
	TraceID           string                         `json:"trace_id" yaml:"trace_id"`
	GroupID           int64                          `json:"group_id" yaml:"group_id"`
	UserID            int64                          `json:"user_id" yaml:"user_id"`
	TriggerMessageID  string                         `json:"trigger_message_id" yaml:"trigger_message_id"`
	Intent            ReplyIntent                    `json:"intent" yaml:"intent"`
	AllowedTools      []string                       `json:"allowed_tools" yaml:"allowed_tools"`
	RetrievedMemories []memorydomain.MemoryRecord    `json:"retrieved_memories" yaml:"retrieved_memories"`
	MediaDescriptors  []mediadomain.MediaDescriptor  `json:"media_descriptors" yaml:"media_descriptors"`
	RetrievedMemes    []mediadomain.MemeSearchResult `json:"retrieved_memes" yaml:"retrieved_memes"`
	Budget            map[string]int                 `json:"budget" yaml:"budget"`
}

type ActionExecution struct {
	ActionID         string                              `json:"action_id" yaml:"action_id"`
	Kind             policydomain.DecisionAction         `json:"kind" yaml:"kind"`
	GroupID          int64                               `json:"group_id" yaml:"group_id"`
	ReplyToMessageID string                              `json:"reply_to_message_id,omitempty" yaml:"reply_to_message_id,omitempty"`
	Segments         []conversationdomain.MessageSegment `json:"segments" yaml:"segments"`
	TargetUserID     int64                               `json:"target_user_id,omitempty" yaml:"target_user_id,omitempty"`
	TargetMessageID  string                              `json:"target_message_id,omitempty" yaml:"target_message_id,omitempty"`
	ReasonCodes      []string                            `json:"reason_codes" yaml:"reason_codes"`
	Meta             map[string]any                      `json:"meta,omitempty" yaml:"meta,omitempty"`
}

type ActionReceipt struct {
	ActionID          string `json:"action_id" yaml:"action_id"`
	PlatformMessageID string `json:"platform_message_id" yaml:"platform_message_id"`
	Sent              bool   `json:"sent" yaml:"sent"`
	// DropReason 记录未发送的原因，空字符串表示正常发送。
	// 可选值: "action_silent"（策略决定沉默）/ "no_content"（内容为空）/ "guard_silenced"（OutputGuard 过滤）
	DropReason string `json:"drop_reason,omitempty" yaml:"drop_reason,omitempty"`
}
