// Package reflection records completed expression outcomes for the next
// presence decision without exposing persistence details to Runtime.
package reflection

import (
	"context"
	"time"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	personasvc "github.com/phlin/go-agent/internal/services/persona"
)

type TurnObserver struct {
	states   ports.RuntimeStateStore
	persona  *personasvc.Service
	cooldown time.Duration
	// policies 解析当前群的 GroupPolicy（静默时段、连续发言上限）。
	policies PolicyResolver
}

// PolicyResolver 是 CanDeliberate 规则闸门需要的群策略视图。
type PolicyResolver interface {
	EffectiveGroupPolicy(groupID int64) policydomain.GroupPolicy
	QuietHourActive(now time.Time, policy policydomain.GroupPolicy) bool
	ActiveHourActive(now time.Time, policy policydomain.GroupPolicy) bool
}

// 时段对发言阈值的影响：深夜不是禁言而是更难开口（真人半夜偶尔还冒
// 一句），活跃时段更容易接话。
const (
	quietHourThresholdPenalty = 0.25
	activeHourThresholdBonus  = 0.10
)

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
		// 静默时段不再硬闸门：深夜只是更难开口（阈值惩罚在
		// DeliberationThreshold 生效），被 @ 的直接触发不受影响。
	}
	return true, nil
}

// DeliberationThreshold 把持久化的人格状态与时段折算进发言阈值：
// tired/withdrawn 抬高（更难开口），talkBias 为正则降低（更愿意接话），
// 深夜时段 +0.25（偶尔还能冒一句），活跃时段 -0.10（更容易接话）。
// 出错时 fail-open 返回 base。
func (o *TurnObserver) DeliberationThreshold(ctx context.Context, groupID int64, base float64) float64 {
	if o == nil {
		return base
	}
	threshold := base
	if o.persona != nil {
		threshold += o.persona.ThresholdAdjustment(ctx, groupID)
	}
	if o.policies != nil {
		now := time.Now()
		policy := o.policies.EffectiveGroupPolicy(groupID)
		if o.policies.QuietHourActive(now, policy) {
			threshold += quietHourThresholdPenalty
		} else if o.policies.ActiveHourActive(now, policy) {
			threshold -= activeHourThresholdBonus
		}
	}
	return min(max(threshold, 0), 1)
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
		personaState, getErr := o.states.GetPersonaState(ctx, snapshot.PersonaProfile.PersonaID, snapshot.Event.GroupID)
		if getErr != nil {
			return getErr
		}
		state.CurrentMood = personaState.Mood
		state.CurrentEnergy = personaState.Energy
	}
	return o.states.SaveRuntimeState(ctx, state)
}
