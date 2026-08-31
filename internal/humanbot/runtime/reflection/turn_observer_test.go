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
	quietHours     []string
	activeHours    []string
	maxConsecutive int
}

func (p *stubPolicy) EffectiveGroupPolicy(groupID int64) policydomain.GroupPolicy {
	return policydomain.GroupPolicy{GroupID: groupID, MaxConsecutiveBot: p.maxConsecutive, QuietHours: p.quietHours, ActiveHours: p.activeHours}
}

func (p *stubPolicy) QuietHourActive(now time.Time, policy policydomain.GroupPolicy) bool {
	return len(policy.QuietHours) > 0
}

func (p *stubPolicy) ActiveHourActive(now time.Time, policy policydomain.GroupPolicy) bool {
	return len(policy.ActiveHours) > 0
}

func TestCanDeliberateBlocksFlood(t *testing.T) {
	store := inmemory.NewStore()
	// 连续发言超上限：ConsecutiveBotTurns=3 >= MaxConsecutiveBot=3
	if err := store.SaveRuntimeState(context.Background(), policydomain.RuntimeState{GroupID: 2, State: policydomain.StateObserving, ConsecutiveBotTurns: 3}); err != nil {
		t.Fatalf("save runtime state: %v", err)
	}
	observer := New(store, nil, 0, &stubPolicy{maxConsecutive: 3})
	if allowed, err := observer.CanDeliberate(context.Background(), 2, time.Now()); err != nil || allowed {
		t.Fatalf("consecutive cap should suppress deliberation: allowed=%v err=%v", allowed, err)
	}
}

func TestQuietHoursSoftenIntoThresholdPenalty(t *testing.T) {
	store := inmemory.NewStore()
	observer := New(store, nil, 0, &stubPolicy{quietHours: []string{"01:00-06:00"}})
	// 深夜不再硬禁言，而是抬高阈值（0.5 -> 0.75）
	if got := observer.DeliberationThreshold(context.Background(), 2, 0.5); got < 0.74 || got > 0.76 {
		t.Fatalf("quiet hour should raise threshold to ~0.75, got %v", got)
	}
	if allowed, err := observer.CanDeliberate(context.Background(), 2, time.Now()); err != nil || !allowed {
		t.Fatalf("quiet hour alone must not hard-block: allowed=%v err=%v", allowed, err)
	}

	// 活跃时段：阈值下浮（0.5 -> 0.4）
	active := New(store, nil, 0, &stubPolicy{activeHours: []string{"19:00-23:00"}})
	if got := active.DeliberationThreshold(context.Background(), 2, 0.5); got < 0.39 || got > 0.41 {
		t.Fatalf("active hour should lower threshold to ~0.4, got %v", got)
	}
}

func TestDeliberationThresholdReflectsPersonaState(t *testing.T) {
	store := inmemory.NewStore()
	ctx := context.Background()
	// persona 状态是全局单槽（GroupID=0）：任意群的阈值查询读同一份情绪
	if err := store.SavePersonaState(ctx, personadomain.PersonaState{PersonaID: "persona-1", GroupID: 0, Mood: "withdrawn", Energy: "tired", TalkBias: 0}); err != nil {
		t.Fatalf("save persona state: %v", err)
	}
	observer := New(store, personasvc.New(store, "persona-1"), 0, nil)
	// withdrawn(+0.15) + tired(+0.2) => 阈值 0.5 -> 0.85
	for _, groupID := range []int64{3, 4} {
		if got := observer.DeliberationThreshold(ctx, groupID, 0.5); got < 0.84 || got > 0.86 {
			t.Fatalf("group %d expected raised threshold ~0.85, got %v", groupID, got)
		}
	}

	// talkBias 为正（想说话）=> 阈值下降；同样全局生效
	if err := store.SavePersonaState(ctx, personadomain.PersonaState{PersonaID: "persona-1", GroupID: 0, Mood: "happy", Energy: "high", TalkBias: 0.4}); err != nil {
		t.Fatalf("save persona state: %v", err)
	}
	if got := observer.DeliberationThreshold(ctx, 5, 0.5); got > 0.15 {
		t.Fatalf("expected lowered threshold <=0.15, got %v", got)
	}
}
