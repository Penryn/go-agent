// Package presence contains the state owned by the human-presence runtime.
package presence

import (
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
)

type EventOrigin string

const (
	OriginInbound  EventOrigin = "inbound"
	OriginOutbound EventOrigin = "outbound"
)

// EventRecord is an immutable observation. Response work may be dropped or
// cancelled, but this fact must remain available for working memory and later
// reflection.
type EventRecord struct {
	EventID    string                               `json:"event_id"`
	GroupID    int64                                `json:"group_id"`
	UserID     int64                                `json:"user_id"`
	Origin     EventOrigin                          `json:"origin"`
	Sequence   uint64                               `json:"sequence"`
	Timestamp  time.Time                            `json:"timestamp"`
	Event      conversationdomain.ConversationEvent `json:"event"`
	RawPayload []byte                               `json:"raw_payload,omitempty"`
}

type ConversationBurst struct {
	UserID    int64     `json:"user_id"`
	EventIDs  []string  `json:"event_ids"`
	Text      string    `json:"text"`
	StartedAt time.Time `json:"started_at"`
	LastAt    time.Time `json:"last_at"`
}

type GroupWorkingMemory struct {
	GroupID       int64                                    `json:"group_id"`
	Version       uint64                                   `json:"version"`
	RecentTail    []EventRecord                            `json:"recent_tail"`
	CurrentBurst  ConversationBurst                        `json:"current_burst"`
	ActiveTopic   string                                   `json:"active_topic"`
	OpenLoops     []string                                 `json:"open_loops"`
	MediaByEvent  map[string][]mediadomain.MediaDescriptor `json:"media_by_event"`
	LastUpdatedAt time.Time                                `json:"last_updated_at"`
	Checkpoint    ProjectionCheckpoint                     `json:"checkpoint"`
	// FeedbackWindows 跟踪最近发送的消息的反馈窗口（最多保留3个）
	FeedbackWindows []FeedbackWindow `json:"feedback_windows,omitempty"`
}

// ProjectionCheckpoint identifies the durable boundary represented by the
// rebuildable working-memory cache.
type ProjectionCheckpoint struct {
	Name      string                           `json:"name"`
	Version   uint64                           `json:"version"`
	Cursor    conversationdomain.ContextCursor `json:"cursor"`
	UpdatedAt time.Time                        `json:"updated_at"`
}
