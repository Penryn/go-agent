package memory

import "time"

type MemoryRecord struct {
	MemoryID           string     `json:"memory_id" yaml:"memory_id"`
	Scope              string     `json:"scope" yaml:"scope"`
	Type               string     `json:"type" yaml:"type"`
	Subject            string     `json:"subject" yaml:"subject"`
	Content            string     `json:"content" yaml:"content"`
	SourceEventID      string     `json:"source_event_id" yaml:"source_event_id"`
	SourceEventIDs     []string   `json:"source_event_ids,omitempty" yaml:"source_event_ids,omitempty"`
	SourceSessionID    string     `json:"source_session_id,omitempty" yaml:"source_session_id,omitempty"`
	Origin             string     `json:"origin,omitempty" yaml:"origin,omitempty"`
	SupersedesMemoryID string     `json:"supersedes_memory_id,omitempty" yaml:"supersedes_memory_id,omitempty"`
	DescriptorRef      string     `json:"descriptor_ref" yaml:"descriptor_ref"`
	Confidence         float64    `json:"confidence" yaml:"confidence"`
	Importance         float64    `json:"importance" yaml:"importance"`
	Revision           int64      `json:"revision" yaml:"revision"`
	CreatedAt          time.Time  `json:"created_at" yaml:"created_at"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
	RecallCount        int        `json:"recall_count" yaml:"recall_count"`
	LastRecalledAt     *time.Time `json:"last_recalled_at,omitempty" yaml:"last_recalled_at,omitempty"`
}

type LearningCandidate struct {
	ID               string     `json:"id" yaml:"id"`
	GroupID          int64      `json:"group_id" yaml:"group_id"`
	Kind             string     `json:"kind" yaml:"kind"`
	Value            string     `json:"value" yaml:"value"`
	Meaning          string     `json:"meaning" yaml:"meaning"`
	EvidenceCount    int        `json:"evidence_count" yaml:"evidence_count"`
	ExampleEventIDs  []string   `json:"example_event_ids" yaml:"example_event_ids"`
	Confidence       float64    `json:"confidence" yaml:"confidence"`
	Status           string     `json:"status" yaml:"status"`
	PromotedMemoryID string     `json:"promoted_memory_id,omitempty" yaml:"promoted_memory_id,omitempty"`
	PromotedAt       *time.Time `json:"promoted_at,omitempty" yaml:"promoted_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at" yaml:"created_at"`
	// TargetUserID 非零时表示该候选属于特定用户（如 user_catchphrase），Scope 写 group:{GroupID}:user:{TargetUserID}。
	// 零值表示群级别候选。
	TargetUserID int64 `json:"target_user_id,omitempty" yaml:"target_user_id,omitempty"`
}

type BehaviorSignal struct {
	Kind      string    `json:"kind" yaml:"kind"`
	Value     string    `json:"value" yaml:"value"`
	Meaning   string    `json:"meaning" yaml:"meaning"`
	EventID   string    `json:"event_id,omitempty" yaml:"event_id,omitempty"`
	Source    string    `json:"source" yaml:"source"`
	Weight    float64   `json:"weight" yaml:"weight"`
	CreatedAt time.Time `json:"created_at" yaml:"created_at"`
}

// LearningWatermark records the last archived fact consumed by one learning
// projector. The timestamp and event ID form a stable cursor when several
// OneBot events share the same second.
type LearningWatermark struct {
	GroupID    int64     `json:"group_id" yaml:"group_id"`
	Kind       string    `json:"kind" yaml:"kind"`
	OccurredAt time.Time `json:"occurred_at" yaml:"occurred_at"`
	EventID    string    `json:"event_id" yaml:"event_id"`
	UpdatedAt  time.Time `json:"updated_at" yaml:"updated_at"`
}
