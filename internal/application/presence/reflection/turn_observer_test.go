package reflection

import (
	"context"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	personasvc "github.com/phlin/go-agent/internal/application/persona"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
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
	maxConsecutive int
	quietHours     []string
}

func (p *stubPolicy) EffectiveGroupPolicy(groupID int64) policydomain.GroupPolicy {
	return policydomain.GroupPolicy{GroupID: groupID, MaxConsecutiveBot: p.maxConsecutive, QuietHours: p.quietHours}
}

func (p *stubPolicy) QuietHourActive(now time.Time, policy policydomain.GroupPolicy) bool {
	for _, quietHour := range policy.QuietHours {
		if matchStubHourRange(now, quietHour) {
			return true
		}
	}
	return false
}

func matchStubHourRange(now time.Time, expr string) bool {
	start, err := time.Parse("15:04", expr[:5])
	if err != nil {
		return false
	}
	end, err := time.Parse("15:04", expr[6:])
	if err != nil {
		return false
	}
	current := now.Hour()*60 + now.Minute()
	s, e := start.Hour()*60+start.Minute(), end.Hour()*60+end.Minute()
	if s <= e {
		return current >= s && current <= e
	}
	return current >= s || current <= e
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

func TestCanDeliberateBlocksQuietHours(t *testing.T) {
	store := inmemory.NewStore()
	observer := New(store, nil, 0, &stubPolicy{quietHours: []string{"23:00-06:00"}})
	// 凌晨 2 点落在跨午夜安静时段内
	night := time.Date(2026, 9, 1, 2, 0, 0, 0, time.Local)
	if allowed, err := observer.CanDeliberate(context.Background(), 3, night); err != nil || allowed {
		t.Fatalf("quiet hours should suppress deliberation: allowed=%v err=%v", allowed, err)
	}
	// 下午 2 点不受限
	day := time.Date(2026, 9, 1, 14, 0, 0, 0, time.Local)
	if allowed, err := observer.CanDeliberate(context.Background(), 3, day); err != nil || !allowed {
		t.Fatalf("daytime should allow deliberation: allowed=%v err=%v", allowed, err)
	}
}
