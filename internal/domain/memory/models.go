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

type BehaviorSignal struct {
	Kind      string    `json:"kind" yaml:"kind"`
	Value     string    `json:"value" yaml:"value"`
	Meaning   string    `json:"meaning" yaml:"meaning"`
	EventID   string    `json:"event_id,omitempty" yaml:"event_id,omitempty"`
	Source    string    `json:"source" yaml:"source"`
	Weight    float64   `json:"weight" yaml:"weight"`
	CreatedAt time.Time `json:"created_at" yaml:"created_at"`
}
