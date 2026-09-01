package prompting

import (
	"context"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

// DeterministicPlanner 只处理不需要 LLM 的动作（poke_back / react / meme_only /
// silent）。需要语言的回复一律依赖模型：模型不可用时保持沉默，本轮放弃，
// 下一轮消息再自然尝试——不说模板话术装作正常。
type DeterministicPlanner struct {
	persona personadomain.PersonaConfig
}

func NewDeterministicPlanner(persona personadomain.PersonaConfig) *DeterministicPlanner {
	return &DeterministicPlanner{persona: persona}
}

func (p *DeterministicPlanner) Plan(_ context.Context, snapshot conversationdomain.ContextSnapshot, decision policydomain.AutonomyDecision) (replydomain.ReplyPlan, error) {
	plan := replydomain.ReplyPlan{
		PlanID:       decision.DecisionID + "-plan",
		SendMode:     "group",
		PlannedActions: []policydomain.DecisionAction{policydomain.ActionSilent},
	}
	if decision.Action == policydomain.ActionSilent {
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

	// reply / poke_reply 等语言动作：模型缺席时沉默，不降级到模板话术。
	return plan, nil
}
