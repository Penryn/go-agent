// Package deliberation owns the seam between presence candidates and model
// planning. Runtime only knows this narrow interface; context and planner
// details stay behind the adapter.
package deliberation

import (
	"context"
	"slices"
	"strings"
	"time"

	contextsvc "github.com/phlin/go-agent/internal/application/context"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

type Input struct {
	Envelope        conversationdomain.EventEnvelope
	Memory          presencedomain.GroupWorkingMemory
	PersonaFeedback []string
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
	snapshot.PersonaFeedback = append([]string(nil), input.PersonaFeedback...)
	decision := decisionFor(input.Envelope)
	if reason := admissionReason(snapshot); reason != "" {
		decision.Action = policydomain.ActionSilent
		decision.Score = 0
		decision.Confidence = 0.95
		decision.ReasonCodes = []string{"admission_gate", reason}
		plan := replydomain.ReplyPlan{
			PlanID:         decision.DecisionID + "-plan",
			PlannedActions: []policydomain.DecisionAction{policydomain.ActionSilent},
			SendMode:       "silent",
		}
		return Result{
			Snapshot: snapshot,
			Decision: decision,
			Plan:     plan,
			Thought: replydomain.ThoughtRecord{
				ThoughtID:      decision.DecisionID + "-thought",
				GroupID:        input.Envelope.Event.GroupID,
				EventID:        input.Envelope.Event.EventID,
				Interpretation: "admission_gate_blocked",
				Evidence:       append([]string(nil), decision.ReasonCodes...),
				Uncertainty:    0.05,
				ChosenAction:   string(decision.Action),
				Outcome:        string(plan.SendMode),
				CreatedAt:      time.Now(),
			},
		}, nil
	}
	plan, err := a.planner.Plan(ctx, snapshot, decision)
	if err != nil {
		return Result{}, err
	}
	// 决策现在由 group_actor 的决策引擎处理
	// 这里仅返回基本结果以保持接口兼容
	return Result{
		Snapshot: snapshot,
		Decision: decision,
		Plan:     plan,
		Thought: replydomain.ThoughtRecord{
			ThoughtID:      decision.DecisionID + "-thought",
			CandidateID:    "",
			GroupID:        input.Envelope.Event.GroupID,
			EventID:        input.Envelope.Event.EventID,
			Interpretation: "delegated_to_decision_engine",
			Evidence:       append([]string(nil), decision.ReasonCodes...),
			Uncertainty:    1.0,
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
		return []policydomain.DecisionAction{policydomain.ActionReply, policydomain.ActionMemeOnly, policydomain.ActionRepair, policydomain.ActionSilent}
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

func decisionFor(envelope conversationdomain.EventEnvelope) policydomain.AutonomyDecision {
	triggerType := "answer"
	if envelope.Event.MentionedBot || envelope.Event.NamedBot || envelope.Event.IsReplyToBot || strings.ContainsAny(envelope.Event.Text, "?？") {
		triggerType = "question"
	}
	return policydomain.AutonomyDecision{
		DecisionID:  envelope.TraceID + "-decision",
		Action:      policydomain.ActionReply,
		TriggerType: triggerType,
		Score:       1.0,
		Confidence:  1.0,
		ReasonCodes: []string{"synchronous_replay"},
	}
}
