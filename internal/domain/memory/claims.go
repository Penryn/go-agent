package memory

import "time"

type ClaimStatus string

const (
	ClaimStaged     ClaimStatus = "staged"
	ClaimConfirmed  ClaimStatus = "confirmed"
	ClaimRejected   ClaimStatus = "rejected"
	ClaimExpired    ClaimStatus = "expired"
	ClaimSuperseded ClaimStatus = "superseded"
)

type MemoryClaim struct {
	ClaimID          string      `json:"claim_id" yaml:"claim_id"`
	Scope            string      `json:"scope" yaml:"scope"`
	Type             string      `json:"type" yaml:"type"`
	Subject          string      `json:"subject" yaml:"subject"`
	Content          string      `json:"content" yaml:"content"`
	EvidenceEventIDs []string    `json:"evidence_event_ids" yaml:"evidence_event_ids"`
	Confidence       float64     `json:"confidence" yaml:"confidence"`
	SuggestedTTL     string      `json:"suggested_ttl,omitempty" yaml:"suggested_ttl,omitempty"`
	Source           string      `json:"source" yaml:"source"`
	Status           ClaimStatus `json:"status" yaml:"status"`
	SupersedesID     string      `json:"supersedes_id,omitempty" yaml:"supersedes_id,omitempty"`
	CreatedAt        time.Time   `json:"created_at" yaml:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at" yaml:"updated_at"`
}
