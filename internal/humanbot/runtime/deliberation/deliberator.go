// Package deliberation owns the seam between presence candidates and model
// planning. Runtime only knows this narrow interface; context and planner
// details stay behind the adapter.
package deliberation

import (
	"context"
	"slices"
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
	decision.Action = resolveAction(input.Candidate.Intent, plan.PlannedActions)
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

// intentBaseline 是各 intent 在 planner 未提议时的默认动作。
var intentBaseline = map[string]policydomain.DecisionAction{
	"react":          policydomain.ActionReact,
	"send_meme":      policydomain.ActionMemeOnly,
	"poke_reply":     policydomain.ActionPokeReply,
	"answer":         policydomain.ActionReply,
	"acknowledge":    policydomain.ActionReply,
	"continue_topic": policydomain.ActionReply,
	"follow_up":      policydomain.ActionReply,
	"question":       policydomain.ActionReply,
	"request_help":   policydomain.ActionReply,
	"support":        policydomain.ActionReply,
	"gratitude":      policydomain.ActionReply,
	"banter":         policydomain.ActionReply,
	"observe_only":   policydomain.ActionSilent,
}

// intentAllowed 限定 planner 可在该 intent 下选择的表达模式；
// 越权提议一律退回 baseline，保证动作权始终在运行时规则手里。
var intentAllowed = map[string][]policydomain.DecisionAction{
	"react":        {policydomain.ActionReact, policydomain.ActionReply, policydomain.ActionMemeOnly, policydomain.ActionSilent},
	"send_meme":    {policydomain.ActionMemeOnly, policydomain.ActionSilent},
	"poke_reply":   {policydomain.ActionPokeReply, policydomain.ActionPokeBack, policydomain.ActionReply, policydomain.ActionMemeOnly, policydomain.ActionSilent},
	"observe_only": {policydomain.ActionSilent},
}

// allowedFor reply 类 intent 共用一条白名单。
func allowedFor(intent string) []policydomain.DecisionAction {
	if allowed, ok := intentAllowed[intent]; ok {
		return allowed
	}
	if _, ok := intentBaseline[intent]; ok {
		return []policydomain.DecisionAction{policydomain.ActionReply, policydomain.ActionMemeOnly, policydomain.ActionSilent}
	}
	return nil
}

func baselineAction(intent string) policydomain.DecisionAction {
	if action, ok := intentBaseline[intent]; ok {
		return action
	}
	return policydomain.ActionSilent
}

// resolveAction keeps policy ownership in the runtime while allowing the
// planner to choose among the expression modes that a candidate permits.
// The executor receives this resolved decision as its sole action authority.
func resolveAction(intent string, proposed []policydomain.DecisionAction) policydomain.DecisionAction {
	baseline := baselineAction(intent)
	if len(proposed) == 0 {
		return baseline
	}
	if slices.Contains(allowedFor(intent), proposed[0]) {
		return proposed[0]
	}
	return baseline
}

func decisionFor(envelope conversationdomain.EventEnvelope, candidate humandomain.ThoughtCandidate) policydomain.AutonomyDecision {
	return policydomain.AutonomyDecision{
		DecisionID:  envelope.TraceID + "-decision",
		StateBefore: policydomain.StateObserving,
		StateAfter:  policydomain.StateCooldown,
		Action:      baselineAction(candidate.Intent),
		TriggerType: candidate.Intent,
		Score:       candidate.Score,
		Confidence:  1 - candidate.Uncertainty,
		ReasonCodes: []string{"thought_candidate", candidate.Intent},
		Explain:     map[string]float64{"candidate_score": candidate.Score},
	}
}
