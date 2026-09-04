package context

import (
	stdcontext "context"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/application/ports"
	groupactor "github.com/phlin/go-agent/internal/application/presence/group_actor"
	"github.com/phlin/go-agent/internal/application/presence/ingress"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
	"github.com/phlin/go-agent/internal/testsupport"
)

type replayMemoryStore struct {
	recent, delta           []conversationdomain.ConversationEvent
	recentCalls, deltaCalls int
}

func (s *replayMemoryStore) ArchiveEvent(stdcontext.Context, conversationdomain.ConversationEvent) error {
	return nil
}
func (s *replayMemoryStore) RecentEvents(stdcontext.Context, int64, int) ([]conversationdomain.ConversationEvent, error) {
	s.recentCalls++
	return s.recent, nil
}
func (s *replayMemoryStore) UpsertMemory(stdcontext.Context, memorydomain.MemoryRecord) error {
	return nil
}
func (s *replayMemoryStore) QueryMemories(stdcontext.Context, ports.MemoryQuery) ([]memorydomain.MemoryRecord, error) {
	return nil, nil
}
func (s *replayMemoryStore) EventsAfter(stdcontext.Context, int64, time.Time, string, int) ([]conversationdomain.ConversationEvent, error) {
	s.deltaCalls++
	return s.delta, nil
}

func TestMergeRecentTurnsOrdersAndBoundsTheProjection(t *testing.T) {
	merged := mergeRecentTurns(
		[]conversationdomain.ConversationEvent{
			{EventID: "b", TimestampUnix: 20},
			{EventID: "d", TimestampUnix: 40},
		},
		[]conversationdomain.ConversationEvent{
			{EventID: "a", TimestampUnix: 10},
			{EventID: "b", TimestampUnix: 20},
			{EventID: "c", TimestampUnix: 30},
		},
		3,
	)
	if len(merged) != 3 || merged[0].EventID != "b" || merged[1].EventID != "c" || merged[2].EventID != "d" {
		t.Fatalf("unexpected recent turns: %+v", merged)
	}
}

func TestRestoreRecentTurnsUsesCheckpointDelta(t *testing.T) {
	ctx := stdcontext.Background()
	store := &replayMemoryStore{delta: []conversationdomain.ConversationEvent{{EventID: "delta", TimestampUnix: 20}}}
	manager := groupactor.NewManager(ingress.NewMemoryEventLog())
	defer manager.Close()
	base := time.Unix(10, 0)
	if _, err := manager.Observe(ctx, presencedomain.EventRecord{
		EventID: "base", GroupID: 1, Timestamp: base,
		Event: conversationdomain.ConversationEvent{EventID: "base", GroupID: 1, TimestampUnix: 10},
	}); err != nil {
		t.Fatal(err)
	}
	service := &Service{memoryStore: store, workingMemory: manager}
	recent, turns, truncated, err := service.restoreRecentTurns(ctx, conversationdomain.EventEnvelope{
		Event: conversationdomain.ConversationEvent{EventID: "current", GroupID: 1, TimestampUnix: 30},
	}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if recent.Version != 1 || len(turns) != 3 || turns[1].EventID != "delta" || turns[2].EventID != "current" || truncated {
		t.Fatalf("unexpected incremental restore: memory=%+v turns=%+v truncated=%v", recent, turns, truncated)
	}
	if store.deltaCalls != 1 || store.recentCalls != 0 {
		t.Fatalf("unexpected archive calls: delta=%d recent=%d", store.deltaCalls, store.recentCalls)
	}
}

func TestRestoreRecentTurnsFallsBackWithoutCheckpoint(t *testing.T) {
	ctx := stdcontext.Background()
	store := &replayMemoryStore{recent: []conversationdomain.ConversationEvent{{EventID: "archived", TimestampUnix: 10}}}
	manager := groupactor.NewManager(ingress.NewMemoryEventLog())
	defer manager.Close()
	service := &Service{memoryStore: store, workingMemory: manager}
	_, turns, _, err := service.restoreRecentTurns(ctx, conversationdomain.EventEnvelope{
		Event: conversationdomain.ConversationEvent{EventID: "current", GroupID: 1, TimestampUnix: 20},
	}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 || turns[0].EventID != "archived" || store.recentCalls != 1 || store.deltaCalls != 0 {
		t.Fatalf("checkpoint fallback failed: turns=%+v recent=%d delta=%d", turns, store.recentCalls, store.deltaCalls)
	}
}

func TestCurrentPersonaFactsRuntimeVerifiedValueOverridesConfigSeed(t *testing.T) {
	store := testsupport.NewStore(t)
	seedAt := time.Date(2026, 9, 1, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	runtimeAt := seedAt.Add(7 * 24 * time.Hour)
	service := &Service{
		persona: personadomain.PersonaConfig{
			ID: "main",
			InitialFacts: []personadomain.PersonaFactSeed{{
				Key:         "school_status",
				Value:       "尚未正式上课",
				EffectiveAt: seedAt.Format(time.RFC3339),
			}},
		},
		personaFacts: store,
	}
	if err := store.AppendPersonaFact(stdcontext.Background(), personadomain.PersonaFact{
		FactID:      "fact-1",
		PersonaID:   "main",
		Key:         "school_status",
		Value:       "已经正式开课",
		Status:      personadomain.PersonaFactVerified,
		SourceKind:  "owner_statement",
		EffectiveAt: runtimeAt,
		RecordedAt:  runtimeAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendPersonaFact(stdcontext.Background(), personadomain.PersonaFact{
		FactID:      "report-1",
		PersonaID:   "main",
		Key:         "school_status",
		Value:       "听说还没开课",
		Status:      personadomain.PersonaFactReported,
		SourceKind:  "group_report",
		EffectiveAt: runtimeAt.Add(-time.Hour),
		RecordedAt:  runtimeAt,
		ExpiresAt:   runtimeAt.Add(24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendPersonaFact(stdcontext.Background(), personadomain.PersonaFact{
		FactID:      "canon-ignored",
		PersonaID:   "main",
		Key:         "school_status",
		Value:       "虚构状态",
		Status:      personadomain.PersonaFactCanon,
		SourceKind:  "self_generated",
		EffectiveAt: runtimeAt.Add(time.Minute),
		RecordedAt:  runtimeAt.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	facts, err := service.currentPersonaFacts(stdcontext.Background(), runtimeAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Value != "已经正式开课" || facts[0].SourceKind != "owner_statement" {
		t.Fatalf("runtime fact did not override config seed: %+v", facts)
	}
}

func TestCurrentPersonaFactsUsesCanonWhenNoVerifiedValueExists(t *testing.T) {
	store := testsupport.NewStore(t)
	now := time.Now().Truncate(time.Second)
	service := &Service{
		persona:      personadomain.PersonaConfig{ID: "main"},
		personaFacts: store,
	}
	if err := store.AppendPersonaFact(stdcontext.Background(), personadomain.PersonaFact{
		FactID: "canon-1", PersonaID: "main", Key: "education.high_school_major", Value: "文科",
		Status: personadomain.PersonaFactCanon, SourceKind: "self_generated",
		EffectiveAt: now, RecordedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	facts, err := service.currentPersonaFacts(stdcontext.Background(), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Status != personadomain.PersonaFactCanon || facts[0].Value != "文科" {
		t.Fatalf("canon was not projected into the next context: %+v", facts)
	}
}
