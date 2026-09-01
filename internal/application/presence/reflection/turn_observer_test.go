package reflection

import (
	"context"
	"testing"
	"time"

	personasvc "github.com/phlin/go-agent/internal/application/persona"
	postgresstore "github.com/phlin/go-agent/internal/adapters/storage/postgres"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	"github.com/phlin/go-agent/internal/testsupport"
)

func TestTurnObserverPersistsCooldownAndPersonaFeedback(t *testing.T) {
	db := testsupport.NewDB(t)
	states := postgresstore.NewStateStore(db)
	observer := New(states, personasvc.New(states, "persona-1"), time.Minute, nil)
	snapshot := conversationdomain.ContextSnapshot{
		Event:       conversationdomain.ConversationEvent{GroupID: 1, MentionedBot: true},
		ActiveTopic: "今晚开黑",
	}
	if err := observer.AfterTurn(context.Background(), snapshot, policydomain.AutonomyDecision{Action: policydomain.ActionReply}, replydomain.ActionReceipt{Sent: true}); err != nil {
		t.Fatalf("after turn: %v", err)
	}
	state, err := states.GetRuntimeState(context.Background(), 1)
	if err != nil {
		t.Fatalf("get runtime state: %v", err)
	}
	if state.State != policydomain.StateCooldown || state.CooldownUntil.Before(time.Now()) || state.LastDirectedAt.IsZero() {
		t.Fatalf("unexpected runtime state: %+v", state)
	}
	if allowed, err := observer.CanDeliberate(context.Background(), 1, time.Now()); err != nil || allowed {
		t.Fatalf("cooldown should suppress deliberation: allowed=%v err=%v", allowed, err)
	}
}

// stubPolicy 让 CanDeliberate 的规则闸门可独立于 config 构造。
type stubPolicy struct {
	maxConsecutive int
}

func (p *stubPolicy) EffectiveGroupPolicy(groupID int64) policydomain.GroupPolicy {
	return policydomain.GroupPolicy{GroupID: groupID, MaxConsecutiveBot: p.maxConsecutive}
}

func TestCanDeliberateBlocksFlood(t *testing.T) {
	states := postgresstore.NewStateStore(testsupport.NewDB(t))
	// 连续发言超上限：ConsecutiveBotTurns=3 >= MaxConsecutiveBot=3
	if err := states.SaveRuntimeState(context.Background(), policydomain.RuntimeState{GroupID: 2, State: policydomain.StateObserving, ConsecutiveBotTurns: 3}); err != nil {
		t.Fatalf("save runtime state: %v", err)
	}
	observer := New(states, nil, 0, &stubPolicy{maxConsecutive: 3})
	if allowed, err := observer.CanDeliberate(context.Background(), 2, time.Now()); err != nil || allowed {
		t.Fatalf("consecutive cap should suppress deliberation: allowed=%v err=%v", allowed, err)
	}
}
