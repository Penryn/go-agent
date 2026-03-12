package profile

import "time"

type MemberStats struct {
	GroupID      int64     `json:"group_id" yaml:"group_id"`
	UserID       int64     `json:"user_id" yaml:"user_id"`
	Nickname     string    `json:"nickname" yaml:"nickname"`
	MessageCount int64     `json:"message_count" yaml:"message_count"`
	LastSpokeAt  time.Time `json:"last_spoke_at" yaml:"last_spoke_at"`
	ActiveScore  float64   `json:"active_score" yaml:"active_score"`
}

type MemberTrait struct {
	GroupID         int64      `json:"group_id" yaml:"group_id"`
	UserID          int64      `json:"user_id" yaml:"user_id"`
	TraitType       string     `json:"trait_type" yaml:"trait_type"`
	Value           string     `json:"value" yaml:"value"`
	Confidence      float64    `json:"confidence" yaml:"confidence"`
	EvidenceEventID string     `json:"evidence_event_id" yaml:"evidence_event_id"`
	UpdatedAt       time.Time  `json:"updated_at" yaml:"updated_at"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
}

type MemberProfile struct {
	Stats         MemberStats   `json:"stats" yaml:"stats"`
	Traits        []MemberTrait `json:"traits" yaml:"traits"`
	Tags          []string      `json:"tags" yaml:"tags"`
	CommonPhrases []string      `json:"common_phrases" yaml:"common_phrases"`
	Interests     []string      `json:"interests" yaml:"interests"`
}

type RelationshipState struct {
	PersonaID      string    `json:"persona_id" yaml:"persona_id"`
	GroupID        int64     `json:"group_id" yaml:"group_id"`
	UserID         int64     `json:"user_id" yaml:"user_id"`
	Familiarity    float64   `json:"familiarity" yaml:"familiarity"`
	Affinity       float64   `json:"affinity" yaml:"affinity"`
	TeaseTolerance float64   `json:"tease_tolerance" yaml:"tease_tolerance"`
	GrudgeScore    float64   `json:"grudge_score" yaml:"grudge_score"`
	LastInteractAt time.Time `json:"last_interact_at" yaml:"last_interact_at"`
}
