package relationship

import "time"

type EventKind string

const (
	EventMessageReceived     EventKind = "message_received"
	EventDirectReply         EventKind = "direct_reply"
	EventPositiveFeedback    EventKind = "positive_feedback"
	EventNegativeFeedback    EventKind = "negative_feedback"
	EventHelpGiven           EventKind = "help_given"
	EventHelpReceived        EventKind = "help_received"
	EventUserCorrection      EventKind = "user_correction"
	EventUserTeasing         EventKind = "user_teasing"
	EventTeasingRejected     EventKind = "user_rejected_teasing"
	EventBotOvertalked       EventKind = "bot_overtalked"
	EventConversationKept    EventKind = "conversation_continued"
	EventConversationDropped EventKind = "conversation_dropped"
)

type Event struct {
	EventID         string    `json:"event_id" yaml:"event_id"`
	PersonaID       string    `json:"persona_id" yaml:"persona_id"`
	GroupID         int64     `json:"group_id" yaml:"group_id"`
	UserID          int64     `json:"user_id" yaml:"user_id"`
	Kind            EventKind `json:"kind" yaml:"kind"`
	Valence         float64   `json:"valence" yaml:"valence"`
	Intensity       float64   `json:"intensity" yaml:"intensity"`
	Reason          string    `json:"reason,omitempty" yaml:"reason,omitempty"`
	EvidenceEventID string    `json:"evidence_event_id,omitempty" yaml:"evidence_event_id,omitempty"`
	DecisionID      string    `json:"decision_id,omitempty" yaml:"decision_id,omitempty"`
	CreatedAt       time.Time `json:"created_at" yaml:"created_at"`
}

type State struct {
	PersonaID      string    `json:"persona_id" yaml:"persona_id"`
	GroupID        int64     `json:"group_id" yaml:"group_id"`
	UserID         int64     `json:"user_id" yaml:"user_id"`
	Familiarity    float64   `json:"familiarity" yaml:"familiarity"`
	Affinity       float64   `json:"affinity" yaml:"affinity"`
	Trust          float64   `json:"trust" yaml:"trust"`
	TeaseTolerance float64   `json:"tease_tolerance" yaml:"tease_tolerance"`
	Friction       float64   `json:"friction" yaml:"friction"`
	LastInteractAt time.Time `json:"last_interact_at" yaml:"last_interact_at"`
	Revision       int64     `json:"revision" yaml:"revision"`
	UpdatedAt      time.Time `json:"updated_at" yaml:"updated_at"`
}

func NewState(personaID string, groupID, userID int64) State {
	return State{
		PersonaID:      personaID,
		GroupID:        groupID,
		UserID:         userID,
		Affinity:       0.25,
		TeaseTolerance: 0.2,
	}
}

func Apply(state State, event Event, now time.Time) State {
	if state.PersonaID == "" {
		state = NewState(event.PersonaID, event.GroupID, event.UserID)
	}
	if now.IsZero() {
		now = time.Now()
	}
	state.LastInteractAt = event.CreatedAt
	if state.LastInteractAt.IsZero() {
		state.LastInteractAt = now
	}
	intensity := clamp01(event.Intensity)
	valence := clamp(event.Valence, -1, 1)
	switch event.Kind {
	case EventMessageReceived:
		state.Familiarity = approach(state.Familiarity, 1, 0.01+0.02*intensity)
	case EventDirectReply, EventConversationKept:
		state.Familiarity = approach(state.Familiarity, 1, 0.01+0.02*intensity)
		state.Affinity = clamp01(state.Affinity + 0.015*max(intensity, 0.5))
	case EventPositiveFeedback, EventHelpReceived:
		state.Affinity = clamp01(state.Affinity + 0.08*max(intensity, 0.5))
		state.Trust = clamp01(state.Trust + 0.04*max(intensity, 0.5))
	case EventNegativeFeedback, EventUserCorrection, EventBotOvertalked:
		state.Affinity = clamp01(state.Affinity - 0.08*max(intensity, 0.5))
		state.Friction = clamp01(state.Friction + 0.1*max(intensity, 0.5))
	case EventHelpGiven:
		state.Trust = clamp01(state.Trust + 0.05*max(intensity, 0.5))
	case EventUserTeasing:
		state.TeaseTolerance = approach(state.TeaseTolerance, 0.8, 0.03*intensity)
	case EventTeasingRejected:
		state.TeaseTolerance = approach(state.TeaseTolerance, 0, 0.12*max(intensity, 0.5))
		state.Friction = clamp01(state.Friction + 0.05*max(intensity, 0.5))
	case EventConversationDropped:
		state.Familiarity = approach(state.Familiarity, 0, 0.002*max(intensity, 0.5))
	}
	if event.Kind != EventMessageReceived && valence != 0 {
		state.Affinity = clamp01(state.Affinity + 0.03*valence*max(intensity, 0.5))
	}
	state.Revision++
	state.UpdatedAt = now
	return state
}

func approach(current, target, step float64) float64 {
	if current < target {
		return min(current+step, target)
	}
	return max(current-step, target)
}

func clamp01(value float64) float64 { return clamp(value, 0, 1) }

func clamp(value, low, high float64) float64 {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}
