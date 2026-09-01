package context

import (
	stdcontext "context"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
)

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

func TestCurrentPersonaFactsRuntimeVerifiedValueOverridesConfigSeed(t *testing.T) {
	store := inmemory.NewStore()
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
	facts, err := service.currentPersonaFacts(stdcontext.Background(), runtimeAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Value != "已经正式开课" || facts[0].SourceKind != "owner_statement" {
		t.Fatalf("runtime fact did not override config seed: %+v", facts)
	}
}
