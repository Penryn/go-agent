// Package domain contains the state owned by the human-presence runtime.
package domain

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
	Candidates    []ThoughtCandidate                       `json:"candidates"`
	MediaByEvent  map[string][]mediadomain.MediaDescriptor `json:"media_by_event"`
	LastUpdatedAt time.Time                                `json:"last_updated_at"`
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
	ReasonCode     string          `json:"reason_code,omitempty"`
	DeliveryTarget string          `json:"delivery_target,omitempty"`
	Status         CandidateStatus `json:"status"`
}
