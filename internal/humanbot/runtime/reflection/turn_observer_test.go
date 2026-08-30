package reflection

import (
	"context"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	personasvc "github.com/phlin/go-agent/internal/services/persona"
)

func TestTurnObserverPersistsCooldownAndPersonaFeedback(t *testing.T) {
	store := inmemory.NewStore()
	observer := New(store, personasvc.New(store, "persona-1"), time.Minute)
	snapshot := conversationdomain.ContextSnapshot{
		Event:          conversationdomain.ConversationEvent{GroupID: 1, MentionedBot: true},
		ActiveTopic:    "今晚开黑",
		PersonaProfile: personadomain.PersonaProfile{PersonaID: "persona-1"},
	}
	if err := observer.AfterTurn(context.Background(), snapshot, policydomain.AutonomyDecision{Action: policydomain.ActionReply}, replydomain.ActionReceipt{Sent: true}); err != nil {
		t.Fatalf("after turn: %v", err)
	}
	state, err := store.GetRuntimeState(context.Background(), 1)
	if err != nil {
		t.Fatalf("get runtime state: %v", err)
	}
	if state.State != policydomain.StateCooldown || state.CooldownUntil.Before(time.Now()) || state.CurrentTopic != "今晚开黑" || state.LastDirectedAt.IsZero() {
		t.Fatalf("unexpected runtime state: %+v", state)
	}
	if allowed, err := observer.CanDeliberate(context.Background(), 1, time.Now()); err != nil || allowed {
		t.Fatalf("cooldown should suppress deliberation: allowed=%v err=%v", allowed, err)
	}
}
