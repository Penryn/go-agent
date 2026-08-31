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
	observer := New(store, personasvc.New(store, "persona-1"), time.Minute, nil)
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

// stubPolicy 让 CanDeliberate 的规则闸门可独立于 config 构造。
type stubPolicy struct {
	quietHours      []string
	maxConsecutive  int
	consecutiveTurn int
}

func (p *stubPolicy) EffectiveGroupPolicy(groupID int64) policydomain.GroupPolicy {
	return policydomain.GroupPolicy{GroupID: groupID, MaxConsecutiveBot: p.maxConsecutive, QuietHours: p.quietHours}
}

func (p *stubPolicy) QuietHourActive(now time.Time, policy policydomain.GroupPolicy) bool {
	return len(policy.QuietHours) > 0
}

func TestCanDeliberateBlocksFloodAndQuietHours(t *testing.T) {
	store := inmemory.NewStore()
	// 连续发言超上限：ConsecutiveBotTurns=3 >= MaxConsecutiveBot=3
	if err := store.SaveRuntimeState(context.Background(), policydomain.RuntimeState{GroupID: 2, State: policydomain.StateObserving, ConsecutiveBotTurns: 3}); err != nil {
		t.Fatalf("save runtime state: %v", err)
	}
	observer := New(store, nil, 0, &stubPolicy{maxConsecutive: 3})
	if allowed, err := observer.CanDeliberate(context.Background(), 2, time.Now()); err != nil || allowed {
		t.Fatalf("consecutive cap should suppress deliberation: allowed=%v err=%v", allowed, err)
	}

	// 静默时段
	quiet := New(store, nil, 0, &stubPolicy{quietHours: []string{"01:00-06:00"}})
	if allowed, err := quiet.CanDeliberate(context.Background(), 2, time.Now()); err != nil || allowed {
		t.Fatalf("quiet hour should suppress deliberation: allowed=%v err=%v", allowed, err)
	}
}

func TestDeliberationThresholdReflectsPersonaState(t *testing.T) {
	store := inmemory.NewStore()
	ctx := context.Background()
	if err := store.SavePersonaState(ctx, personadomain.PersonaState{PersonaID: "persona-1", GroupID: 3, Mood: "withdrawn", Energy: "tired", TalkBias: 0}); err != nil {
		t.Fatalf("save persona state: %v", err)
	}
	observer := New(store, personasvc.New(store, "persona-1"), 0, nil)
	// withdrawn(+0.15) + tired(+0.2) => 阈值 0.5 -> 0.85
	if got := observer.DeliberationThreshold(ctx, 3, 0.5); got < 0.84 || got > 0.86 {
		t.Fatalf("expected raised threshold ~0.85, got %v", got)
	}

	// talkBias 为正（想说话）=> 阈值下降
	if err := store.SavePersonaState(ctx, personadomain.PersonaState{PersonaID: "persona-1", GroupID: 4, Mood: "happy", Energy: "high", TalkBias: 0.4}); err != nil {
		t.Fatalf("save persona state: %v", err)
	}
	if got := observer.DeliberationThreshold(ctx, 4, 0.5); got > 0.15 {
		t.Fatalf("expected lowered threshold <=0.15, got %v", got)
	}
}
