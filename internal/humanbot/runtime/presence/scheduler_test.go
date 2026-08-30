package presence

import (
	"testing"
	"time"

	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
)

func TestSchedulerSelectsDueUrgentCandidate(t *testing.T) {
	now := time.Unix(100, 0)
	memory := humandomain.GroupWorkingMemory{Candidates: []humandomain.ThoughtCandidate{
		{CandidateID: "late", Status: humandomain.CandidatePending, Urgency: 1, DueAt: now.Add(time.Second), ExpiresAt: now.Add(time.Minute)},
		{CandidateID: "urgent", Status: humandomain.CandidatePending, Urgency: 0.8, DueAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute)},
		{CandidateID: "expired", Status: humandomain.CandidatePending, Urgency: 2, DueAt: now.Add(-time.Minute), ExpiresAt: now.Add(-time.Second)},
	}}
	selected, ok := NewScheduler(1).Select(now, memory, humandomain.PresenceState{})
	if !ok || selected.CandidateID != "urgent" {
		t.Fatalf("unexpected selection: %+v, ok=%v", selected, ok)
	}
}

func TestSchedulerRespectsCognitiveLoad(t *testing.T) {
	now := time.Unix(100, 0)
	memory := humandomain.GroupWorkingMemory{Candidates: []humandomain.ThoughtCandidate{{CandidateID: "c", DueAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute), Status: humandomain.CandidatePending}}}
	if _, ok := NewScheduler(1).Select(now, memory, humandomain.PresenceState{CognitiveLoad: 1}); ok {
		t.Fatal("expected no selection at cognitive load limit")
	}
}
