// Package presence selects when a group should spend attention on a thought.
package presence

import (
	"time"

	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
)

type Scheduler struct {
	MaxCognitiveLoad float64
}

func NewScheduler(maxLoad float64) Scheduler {
	if maxLoad <= 0 {
		maxLoad = 1
	}
	return Scheduler{MaxCognitiveLoad: maxLoad}
}

// Select returns the highest-scoring due candidate. It never mutates working
// memory, so the group actor remains the owner of candidate state transitions.
func (s Scheduler) Select(now time.Time, memory humandomain.GroupWorkingMemory, state humandomain.PresenceState) (humandomain.ThoughtCandidate, bool) {
	if state.CognitiveLoad >= s.MaxCognitiveLoad {
		return humandomain.ThoughtCandidate{}, false
	}
	var selected humandomain.ThoughtCandidate
	found := false
	for _, candidate := range memory.Candidates {
		if candidate.Status != humandomain.CandidatePending && candidate.Status != humandomain.CandidateDeferred {
			continue
		}
		if now.Before(candidate.DueAt) || !now.Before(candidate.ExpiresAt) {
			continue
		}
		if !found || candidate.Urgency > selected.Urgency || (candidate.Urgency == selected.Urgency && candidate.DueAt.Before(selected.DueAt)) {
			selected = candidate
			found = true
		}
	}
	return selected, found
}
