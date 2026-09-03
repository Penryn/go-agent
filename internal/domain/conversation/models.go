package conversation

import (
	"time"

	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
)

type EventKind string

const (
	EventMessage EventKind = "message"
	EventNotice  EventKind = "notice"
	EventPoke    EventKind = "poke"
	EventRecall  EventKind = "recall"
	EventMeta    EventKind = "meta_event"
)

type MessageSegment struct {
	Type string         `json:"type" yaml:"type"`
	Data map[string]any `json:"data" yaml:"data"`
}

// SenderIdentity preserves both platform-level names. GroupCard is scoped to
// one QQ group while QQNickname is the account nickname. DisplayName is a
// deterministic convenience projection and must not replace either source.
type SenderIdentity struct {
	QQNickname  string `json:"qq_nickname,omitempty" yaml:"qq_nickname,omitempty"`
	GroupCard   string `json:"group_card,omitempty" yaml:"group_card,omitempty"`
	DisplayName string `json:"display_name,omitempty" yaml:"display_name,omitempty"`
}

type ConversationEvent struct {
	EventID          string                             `json:"event_id" yaml:"event_id"`
	GroupID          int64                              `json:"group_id" yaml:"group_id"`
	UserID           int64                              `json:"user_id" yaml:"user_id"`
	Sender           SenderIdentity                     `json:"sender,omitempty" yaml:"sender,omitempty"`
	MessageID        string                             `json:"message_id" yaml:"message_id"`
	ReplyToMessageID string                             `json:"reply_to_message_id,omitempty" yaml:"reply_to_message_id,omitempty"`
	Kind             EventKind                          `json:"kind" yaml:"kind"`
	Text             string                             `json:"text" yaml:"text"`
	Segments         []MessageSegment                   `json:"segments,omitempty" yaml:"segments,omitempty"`
	MentionedBot     bool                               `json:"mentioned_bot" yaml:"mentioned_bot"`
	NamedBot         bool                               `json:"named_bot" yaml:"named_bot"`
	IsReplyToBot     bool                               `json:"is_reply_to_bot" yaml:"is_reply_to_bot"`
	Attachments      []mediadomain.MultimodalAttachment `json:"attachments,omitempty" yaml:"attachments,omitempty"`
	TimestampUnix    int64                              `json:"timestamp_unix" yaml:"timestamp_unix"`
}

type EventEnvelope struct {
	Source        string            `json:"source" yaml:"source"`
	SelfID        int64             `json:"self_id" yaml:"self_id"`
	ReceivedAt    time.Time         `json:"received_at" yaml:"received_at"`
	RawPayload    []byte            `json:"raw_payload,omitempty" yaml:"raw_payload,omitempty"`
	Event         ConversationEvent `json:"event" yaml:"event"`
	TraceID       string            `json:"trace_id" yaml:"trace_id"`
	CorrelationID string            `json:"correlation_id" yaml:"correlation_id"`
}

// ThoughtDigest 是 ThoughtRecord 的窄投影：只保留模型回看上次判断
// 需要的字段，避免 conversation 反向依赖 reply 包。
type ThoughtDigest struct {
	Interpretation string    `json:"interpretation" yaml:"interpretation"`
	ChosenAction   string    `json:"chosen_action" yaml:"chosen_action"`
	Outcome        string    `json:"outcome" yaml:"outcome"`
	CreatedAt      time.Time `json:"created_at" yaml:"created_at"`
}

// PromptMessage is the model-visible part of one persisted conversation.
// Provider response metadata is intentionally excluded so the session stays
// portable across model adapters.
type PromptMessage struct {
	Role       string           `json:"role" yaml:"role"`
	Content    string           `json:"content" yaml:"content"`
	Name       string           `json:"name,omitempty" yaml:"name,omitempty"`
	ToolCalls  []PromptToolCall `json:"tool_calls,omitempty" yaml:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty" yaml:"tool_call_id,omitempty"`
}

type PromptToolCall struct {
	ID        string `json:"id" yaml:"id"`
	Type      string `json:"type" yaml:"type"`
	Name      string `json:"name" yaml:"name"`
	Arguments string `json:"arguments" yaml:"arguments"`
}

// PromptSession contains the exact message history reused on the next turn.
// Version changes intentionally invalidate old provider prefixes.
type PromptSession struct {
	Version  string          `json:"version,omitempty" yaml:"version,omitempty"`
	Messages []PromptMessage `json:"messages,omitempty" yaml:"messages,omitempty"`
}

type ContextSnapshot struct {
	SnapshotID       string                      `json:"snapshot_id" yaml:"snapshot_id"`
	SelfID           int64                       `json:"self_id" yaml:"self_id"`
	Projection       ProjectionMetadata          `json:"projection" yaml:"projection"`
	Event            ConversationEvent           `json:"event" yaml:"event"`
	RecentTurns      []ConversationEvent         `json:"recent_turns" yaml:"recent_turns"`
	PromptSession    PromptSession               `json:"prompt_session,omitempty" yaml:"prompt_session,omitempty"`
	RelevantMemories []memorydomain.MemoryRecord `json:"relevant_memories" yaml:"relevant_memories"`
	// RecentThoughts 是该群最近几轮的思考摘要（新到旧），供模型回看自己
	// 上次的判断——说错过的话别再说，收过的梗换着接。
	RecentThoughts    []ThoughtDigest                 `json:"recent_thoughts,omitempty" yaml:"recent_thoughts,omitempty"`
	MediaDescriptors  []mediadomain.MediaDescriptor   `json:"media_descriptors" yaml:"media_descriptors"`
	ActiveTopic       string                          `json:"active_topic,omitempty" yaml:"active_topic,omitempty"`
	OpenLoops         []string                        `json:"open_loops,omitempty" yaml:"open_loops,omitempty"`
	MemberProfile     profiledomain.MemberProfile     `json:"member_profile" yaml:"member_profile"`
	RelationshipState profiledomain.RelationshipState `json:"relationship_state" yaml:"relationship_state"`
	PersonaState      personadomain.PersonaState      `json:"persona_state" yaml:"persona_state"`
	PersonaView       personadomain.PersonaView       `json:"persona_view" yaml:"persona_view"`
	PersonaFacts      []personadomain.PersonaFact     `json:"persona_facts,omitempty" yaml:"persona_facts,omitempty"`
	GroupPolicy       policydomain.GroupPolicy        `json:"group_policy" yaml:"group_policy"`
	RuntimeState      policydomain.RuntimeState       `json:"runtime_state" yaml:"runtime_state"`
	DecisionHints     []string                        `json:"decision_hints" yaml:"decision_hints"`
	PersonaFeedback   []string                        `json:"persona_feedback,omitempty" yaml:"persona_feedback,omitempty"`
}

// ContextCursor identifies the last archived fact included in a snapshot.
// It is independent from SnapshotID so a projector can resume after restart.
type ContextCursor struct {
	EventID       string `json:"event_id" yaml:"event_id"`
	TimestampUnix int64  `json:"timestamp_unix" yaml:"timestamp_unix"`
}

// ProjectionMetadata describes how the live context was assembled. The
// metadata is a seam for future summary/checkpoint projections.
type ProjectionMetadata struct {
	Name            string        `json:"name" yaml:"name"`
	Version         uint64        `json:"version" yaml:"version"`
	Cursor          ContextCursor `json:"cursor" yaml:"cursor"`
	Complete        bool          `json:"complete" yaml:"complete"`
	RecentTruncated bool          `json:"recent_truncated" yaml:"recent_truncated"`
}
