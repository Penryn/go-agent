// Package domain contains the state owned by the human-presence runtime.
package domain

import (
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
)

type EventOrigin string

const (
	OriginInbound  EventOrigin = "inbound"
	OriginOutbound EventOrigin = "outbound"
	OriginSystem   EventOrigin = "system"
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
	GroupID       int64              `json:"group_id"`
	Version       uint64             `json:"version"`
	RecentTail    []EventRecord      `json:"recent_tail"`
	CurrentBurst  ConversationBurst  `json:"current_burst"`
	ActiveTopic   string             `json:"active_topic"`
	OpenLoops     []string           `json:"open_loops"`
	Candidates    []ThoughtCandidate `json:"candidates"`
	LastUpdatedAt time.Time          `json:"last_updated_at"`
}

type CandidateStatus string

const (
	CandidatePending   CandidateStatus = "pending"
	CandidateDeferred  CandidateStatus = "deferred"
	CandidateAccepted  CandidateStatus = "accepted"
	CandidateCompleted CandidateStatus = "completed"
	CandidateCancelled CandidateStatus = "cancelled"
	CandidateExpired   CandidateStatus = "expired"
)

type ThoughtCandidate struct {
	CandidateID    string          `json:"candidate_id"`
	SourceEventIDs []string        `json:"source_event_ids"`
	TopicID        string          `json:"topic_id"`
	Addressee      int64           `json:"addressee"`
	Intent         string          `json:"intent"`
	Urgency        float64         `json:"urgency"`
	Score          float64         `json:"score"`
	DueAt          time.Time       `json:"due_at"`
	ExpiresAt      time.Time       `json:"expires_at"`
	Uncertainty    float64         `json:"uncertainty"`
	Status         CandidateStatus `json:"status"`
}

type PresenceMode string

const (
	ModeObserving  PresenceMode = "observing"
	ModeListening  PresenceMode = "listening"
	ModeThinking   PresenceMode = "thinking"
	ModeScheduled  PresenceMode = "scheduled"
	ModeExpressing PresenceMode = "expressing"
	ModeCooldown   PresenceMode = "cooldown"
	ModeResting    PresenceMode = "resting"
)

type PresenceState struct {
	GroupID       int64        `json:"group_id"`
	Mode          PresenceMode `json:"mode"`
	LastSpokeAt   time.Time    `json:"last_spoke_at"`
	Attention     float64      `json:"attention"`
	CognitiveLoad float64      `json:"cognitive_load"`
}
