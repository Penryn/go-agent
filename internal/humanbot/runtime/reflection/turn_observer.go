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
}

func New(states ports.RuntimeStateStore, persona *personasvc.Service, cooldown time.Duration) *TurnObserver {
	if cooldown < 0 {
		cooldown = 0
	}
	return &TurnObserver{states: states, persona: persona, cooldown: cooldown}
}

func (o *TurnObserver) CanDeliberate(ctx context.Context, groupID int64, now time.Time) (bool, error) {
	if o == nil || o.states == nil {
		return true, nil
	}
	state, err := o.states.GetRuntimeState(ctx, groupID)
	if err != nil {
		return false, err
	}
	return state.CooldownUntil.IsZero() || !now.Before(state.CooldownUntil), nil
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
