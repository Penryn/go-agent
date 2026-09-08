package relationship

import (
	"testing"
	"time"
)

func TestApplyRelationshipSignals(t *testing.T) {
	state := NewState("persona", 1, 2)
	state = Apply(state, Event{PersonaID: "persona", GroupID: 1, UserID: 2, Kind: EventPositiveFeedback, Intensity: 1}, now())
	if state.Affinity <= 0.25 || state.Trust <= 0 {
		t.Fatalf("positive feedback did not improve relationship: %+v", state)
	}
	state = Apply(state, Event{PersonaID: "persona", GroupID: 1, UserID: 2, Kind: EventTeasingRejected, Intensity: 1}, now())
	if state.TeaseTolerance >= 0.2 || state.Friction <= 0 {
		t.Fatalf("rejected teasing did not lower tolerance or add friction: %+v", state)
	}
}

func now() (result time.Time) { return time.Unix(100, 0) }
