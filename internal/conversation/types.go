package conversation

import "time"

// Event is a compact relation hint for an event already present in the
// visible history or current batch. Event text stays in the normal inputs.
type Event struct {
	EventID   string   `json:"event_id"`
	MessageID string   `json:"message_id,omitempty"`
	UserID    int64    `json:"user_id"`
	Relation  string   `json:"relation"`
	TopicIDs  []string `json:"topic_ids,omitempty"`
}

// Topic is a bounded view of one parallel thread. Event text remains in
// history and batch; this object only carries grouping signals.
type Topic struct {
	ID              string   `json:"topic_id"`
	State           string   `json:"state"`
	Participants    []int64  `json:"participants"`
	EventIDs        []string `json:"event_ids"`
	BotParticipated bool     `json:"bot_participated"`
	Summary         string   `json:"summary,omitempty"`
}

// Context gives the model explicit participation cues without a second model
// call or a persistent topic graph.
type Context struct {
	Continuing bool    `json:"continuing"`
	Topics     []Topic `json:"topics,omitempty"`
	Batch      []Event `json:"batch"`
	History    []Event `json:"history"`
}

// TopicEventView is one persisted ownership row. TopicIDs are the candidates
// recorded for the event; facts about the event live in the messages table.
type TopicEventView struct {
	TopicID    string
	EventID    string
	MessageID  string
	Seq        int64
	AssignedAt time.Time
	TopicIDs   []string
}

// TopicSummary is a versioned, evidence-backed topic summary.
type TopicSummary struct {
	GroupID     int64
	TopicID     string
	Revision    int64
	Summary     string
	EvidenceIDs []string
	SourceSeq   int64
	UpdatedAt   time.Time
}
