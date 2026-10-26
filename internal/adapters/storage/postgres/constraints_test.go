package postgresstore

import (
	"context"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/application/ports"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

func TestConstraintsUseCurrentMemorySchemaAndLatestScopedPreference(t *testing.T) {
	store := NewStore(setupPostgres(t))
	ctx := context.Background()
	expired := time.Now().Add(-time.Hour)
	old := memorydomain.MemoryRecord{MemoryID: "old", Scope: "group:1", Subject: "7", Predicate: memorydomain.PredicateAllowPoke, NormalizedValue: "false", CreatedAt: time.Now().Add(-time.Minute), Type: "social", Content: "old", Qualifier: "default"}
	for _, record := range []memorydomain.MemoryRecord{
		old,
		{MemoryID: "new", Scope: "group:1", Subject: "7", Predicate: memorydomain.PredicateAllowPoke, NormalizedValue: "true"},
		{MemoryID: "expired", Scope: "group:1", Subject: "7", Predicate: memorydomain.PredicateAllowMention, NormalizedValue: "false", ExpiresAt: &expired},
		{MemoryID: "other-member", Scope: "group:1", Subject: "8", Predicate: memorydomain.PredicateAllowPoke, NormalizedValue: "false"},
		{MemoryID: "other-group", Scope: "group:2", Subject: "7", Predicate: memorydomain.PredicateAllowPoke, NormalizedValue: "false"},
	} {
		record.Type, record.Content, record.Qualifier = "social", record.MemoryID, "default"
		if err := store.UpsertMemory(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	// Retrying the older extraction must not restore a superseded restriction.
	if err := store.UpsertMemory(ctx, old); err != nil {
		t.Fatal(err)
	}
	constraints, err := store.GetConstraints(ctx, "group:1", []string{"7"})
	if err != nil || len(constraints) != 1 || constraints[0].Type != "allow_poke" || constraints[0].Value != "true" {
		t.Fatalf("constraints diverged from authoritative memory: %+v err=%v", constraints, err)
	}
	records, err := store.QueryMemories(ctx, ports.MemoryQuery{GroupID: 1, Scope: "group:1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.MemoryID == "new" {
			if record.Predicate != memorydomain.PredicateAllowPoke || record.NormalizedValue != "true" || record.Qualifier != "default" {
				t.Fatalf("retrieval lost structured preference: %+v", record)
			}
			return
		}
	}
	t.Fatal("preference missing from retrieval")
}
