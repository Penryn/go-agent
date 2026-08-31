// Package deliberation owns the seam between presence candidates and model
// planning. Runtime only knows this narrow interface; context and planner
// details stay behind the adapter.
package deliberation

import (
	"context"
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
	contextsvc "github.com/phlin/go-agent/internal/services/context"
)

type Input struct {
	Envelope  conversationdomain.EventEnvelope
	Candidate humandomain.ThoughtCandidate
	Memory    humandomain.GroupWorkingMemory
}

type Result struct {
	Snapshot conversationdomain.ContextSnapshot
	Decision policydomain.AutonomyDecision
	Plan     replydomain.ReplyPlan
	Thought  replydomain.ThoughtRecord
}

type Deliberator interface {
	Deliberate(context.Context, Input) (Result, error)
}

// Planner is the existing model-planning leaf contract. It stays behind this
// adapter so the Runtime does not depend on the wide snapshot.
type Planner interface {
	Plan(context.Context, conversationdomain.ContextSnapshot, policydomain.AutonomyDecision) (replydomain.ReplyPlan, error)
}

type Adapter struct {
	context *contextsvc.Service
	planner Planner
}

func NewAdapter(contextService *contextsvc.Service, planner Planner) *Adapter {
	return &Adapter{context: contextService, planner: planner}
}

func (a *Adapter) Deliberate(ctx context.Context, input Input) (Result, error) {
	snapshot, err := a.context.BuildSnapshot(ctx, input.Envelope, nil)
	if err != nil {
		return Result{}, err
	}
	decision := decisionFor(input.Envelope, input.Candidate)
	plan, err := a.planner.Plan(ctx, snapshot, decision)
	if err != nil {
		return Result{}, err
	}
	decision.Action = resolveAction(input.Candidate.Intent, decision.Action, plan.PlannedActions)
	return Result{
		Snapshot: snapshot,
		Decision: decision,
		Plan:     plan,
		Thought: replydomain.ThoughtRecord{
			ThoughtID:      decision.DecisionID + "-thought",
			CandidateID:    input.Candidate.CandidateID,
			GroupID:        input.Envelope.Event.GroupID,
			EventID:        input.Envelope.Event.EventID,
			Interpretation: input.Candidate.Intent,
			Evidence:       append([]string(nil), decision.ReasonCodes...),
			Uncertainty:    input.Candidate.Uncertainty,
			ChosenAction:   string(decision.Action),
			Outcome:        string(plan.SendMode),
			CreatedAt:      time.Now(),
		},
	}, nil
}

// resolveAction keeps policy ownership in the runtime while allowing the
// planner to choose among the expression modes that a candidate permits.
// The executor receives this resolved decision as its sole action authority.
func resolveAction(intent string, fallback policydomain.DecisionAction, proposed []policydomain.DecisionAction) policydomain.DecisionAction {
	if len(proposed) == 0 {
		return fallback
	}
	action := proposed[0]
	allowed := func(actions ...policydomain.DecisionAction) bool {
		for _, candidate := range actions {
			if action == candidate {
				return true
			}
		}
		return false
	}
	switch intent {
	case "answer", "acknowledge", "continue_topic", "follow_up", "question", "request_help", "support", "gratitude", "banter":
		if allowed(policydomain.ActionReply, policydomain.ActionMemeOnly, policydomain.ActionSilent) {
			return action
		}
	case "react":
		if allowed(policydomain.ActionReact, policydomain.ActionReply, policydomain.ActionMemeOnly, policydomain.ActionSilent) {
			return action
		}
	case "send_meme":
		if allowed(policydomain.ActionMemeOnly, policydomain.ActionSilent) {
			return action
		}
	case "poke_reply":
		if allowed(policydomain.ActionPokeReply, policydomain.ActionReply, policydomain.ActionMemeOnly, policydomain.ActionSilent) {
			return action
		}
	case "observe_only":
		return policydomain.ActionSilent
	}
	return fallback
}

func decisionFor(envelope conversationdomain.EventEnvelope, candidate humandomain.ThoughtCandidate) policydomain.AutonomyDecision {
	action := policydomain.ActionReply
	switch candidate.Intent {
	case "observe_only":
		action = policydomain.ActionSilent
	case "react":
		action = policydomain.ActionReact
	case "send_meme":
		action = policydomain.ActionMemeOnly
	case "answer", "acknowledge", "continue_topic", "follow_up", "question", "request_help", "support", "gratitude", "banter":
		action = policydomain.ActionReply
	case "poke_reply":
		action = policydomain.ActionPokeReply
	default:
		action = policydomain.ActionSilent
	}
	return policydomain.AutonomyDecision{
		DecisionID:  envelope.TraceID + "-decision",
		StateBefore: policydomain.StateObserving,
		StateAfter:  policydomain.StateCooldown,
		Action:      action,
		TriggerType: candidate.Intent,
		Score:       candidate.Score,
		Confidence:  1 - candidate.Uncertainty,
		ReasonCodes: []string{"thought_candidate", candidate.Intent},
		Explain:     map[string]float64{"candidate_score": candidate.Score},
	}
}
