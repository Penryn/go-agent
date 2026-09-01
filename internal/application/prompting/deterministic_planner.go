package prompting

import (
	"context"
	"math/rand"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

// pokeReplyFallbacks 是 ActionPokeReply LLM 失败时的通用降级气泡候选。
var pokeReplyFallbacks = []string{
	"？",
	"嗯？",
	"怎么了？",
	"……",
	"有事吗？",
}

type DeterministicPlanner struct {
	persona personadomain.PersonaConfig
}

func NewDeterministicPlanner(persona personadomain.PersonaConfig) *DeterministicPlanner {
	return &DeterministicPlanner{persona: persona}
}

func (p *DeterministicPlanner) Plan(_ context.Context, snapshot conversationdomain.ContextSnapshot, decision policydomain.AutonomyDecision) (replydomain.ReplyPlan, error) {
	maxChars, _ := replyBudget(p.persona, snapshot, decision.TriggerType)
	fallbackText := fallbackExpression(p.persona, decision.TriggerType)
	plan := replydomain.ReplyPlan{
		PlanID:       decision.DecisionID + "-plan",
		SendMode:     "group",
		FallbackText: fallbackText,
	}

	if decision.Action == policydomain.ActionSilent {
		plan.PlannedActions = []policydomain.DecisionAction{policydomain.ActionSilent}
		return plan, nil
	}

	// ActionPokeBack 不需要 LLM，直接填入目标用户 ID 返回
	if decision.Action == policydomain.ActionPokeBack {
		plan.PlannedActions = []policydomain.DecisionAction{policydomain.ActionPokeBack}
		plan.ActionParams = map[string]any{
			"user_id": snapshot.Event.UserID,
		}
		return plan, nil
	}

	if decision.Action == policydomain.ActionReact {
		plan.PlannedActions = []policydomain.DecisionAction{policydomain.ActionReact}
		plan.ActionParams = map[string]any{
			"message_id": snapshot.Event.MessageID,
			// NapCat accepts an empty emoji id only for adapters that choose a
			// platform default; keep the action explicit for future model choice.
			"emoji_id": "",
		}
		return plan, nil
	}

	if decision.Action == policydomain.ActionMemeOnly {
		plan.PlannedActions = []policydomain.DecisionAction{policydomain.ActionMemeOnly}
		return plan, nil
	}

	// ActionPokeReply：被戳后用对话回复，LLM 失败时随机选一条降级气泡
	if decision.Action == policydomain.ActionPokeReply {
		plan.PlannedActions = []policydomain.DecisionAction{policydomain.ActionPokeReply}
		plan.Bubbles = []string{pokeReplyFallbacks[rand.Intn(len(pokeReplyFallbacks))]}
		plan.Intent = replydomain.ReplyIntent{
			Kind:            "chat",
			Goal:            "自然回应被戳",
			TargetUserIDs:   []int64{snapshot.Event.UserID},
			PreferShortText: true,
			MaxChars:        maxChars,
		}
		return plan, nil
	}

	plan.Intent = replydomain.ReplyIntent{
		Kind:            "chat",
		Goal:            dialogueGoal(decision.TriggerType),
		TargetUserIDs:   []int64{snapshot.Event.UserID},
		PreferShortText: true,
		MaxChars:        maxChars,
	}
	plan.PlannedActions = []policydomain.DecisionAction{policydomain.ActionReply}

	plan.Bubbles = []string{fallbackText}

	return plan, nil
}
