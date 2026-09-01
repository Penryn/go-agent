// Package reflection records completed expression outcomes for the next
// presence decision without exposing persistence details to Runtime.
package reflection

import (
	"context"
	"time"

	personasvc "github.com/phlin/go-agent/internal/application/persona"
	"github.com/phlin/go-agent/internal/application/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

type TurnObserver struct {
	states   ports.RuntimeStateStore
	persona  *personasvc.Service
	cooldown time.Duration
	// policies 解析当前群的 GroupPolicy（静默时段、连续发言上限）。
	policies PolicyResolver
}

// PolicyResolver 是 CanDeliberate 输出规则闸门需要的群策略视图。
type PolicyResolver interface {
	EffectiveGroupPolicy(groupID int64) policydomain.GroupPolicy
	QuietHourActive(now time.Time, policy policydomain.GroupPolicy) bool
	ActiveHourActive(now time.Time, policy policydomain.GroupPolicy) bool
}

func New(states ports.RuntimeStateStore, persona *personasvc.Service, cooldown time.Duration, policies PolicyResolver) *TurnObserver {
	if cooldown < 0 {
		cooldown = 0
	}
	return &TurnObserver{states: states, persona: persona, cooldown: cooldown, policies: policies}
}

func (o *TurnObserver) CanDeliberate(ctx context.Context, groupID int64, now time.Time) (bool, error) {
	if o == nil || o.states == nil {
		return true, nil
	}
	state, err := o.states.GetRuntimeState(ctx, groupID)
	if err != nil {
		return false, err
	}
	if state.CooldownUntil.After(now) {
		return false, nil
	}
	// 防刷屏：连续发言超过群策略上限后，等待一次自然的群消息间隔。
	if o.policies != nil {
		policy := o.policies.EffectiveGroupPolicy(groupID)
		if maxConsecutive := policy.MaxConsecutiveBot; maxConsecutive > 0 && state.ConsecutiveBotTurns >= maxConsecutive {
			return false, nil
		}
	}
	return true, nil
}

func (o *TurnObserver) AfterTurn(ctx context.Context, snapshot conversationdomain.ContextSnapshot, decision policydomain.AutonomyDecision, receipt replydomain.ActionReceipt) error {
	if o == nil || o.states == nil {
		return nil
	}
	if o.persona != nil {
		if err := o.persona.UpdateAfterTurn(ctx, snapshot, decision, receipt.Sent); err != nil {
			return err
		}
	}

	state, err := o.states.GetRuntimeState(ctx, snapshot.Event.GroupID)
	if err != nil {
		return err
	}
	now := time.Now()
	state.GroupID = snapshot.Event.GroupID
	state.CurrentTopic = snapshot.ActiveTopic
	state.State = policydomain.StateObserving
	if receipt.Sent {
		state.State = policydomain.StateCooldown
		state.LastBotSpeakAt = now
		state.ConsecutiveBotTurns++
		state.RepliesLast10Min++
		state.CooldownUntil = now.Add(o.cooldown)
	}
	if snapshot.Event.MentionedBot || snapshot.Event.NamedBot || snapshot.Event.IsReplyToBot {
		state.LastDirectedAt = now
	}
	if o.persona != nil {
		// persona 状态全局单槽（GroupID=0），与 context/persona 服务一致
		personaState, getErr := o.states.GetPersonaState(ctx, snapshot.PersonaProfile.PersonaID, 0)
		if getErr != nil {
			return getErr
		}
		state.CurrentMood = personaState.Mood
		state.CurrentEnergy = personaState.Energy
	}
	return o.states.SaveRuntimeState(ctx, state)
}
