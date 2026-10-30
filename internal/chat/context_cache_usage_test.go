package chat

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/cache"
	ctxsnapshot "github.com/phlin/go-agent/internal/context"
	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/qq"
)

// noteStore is the smallest chat.Store + memory.Store that can answer one
// recall. It avoids importing the replay package (which imports this one).
type noteStore struct {
	notes memory.Note
	used  []string
}

func (s *noteStore) ActiveNotes(context.Context, int64, int) ([]memory.Note, error) {
	return []memory.Note{s.notes}, nil
}
func (s *noteStore) VectorNotes(context.Context, int64, []float32, string, int, float64) ([]memory.Note, error) {
	return nil, nil
}
func (s *noteStore) SaveNotes(context.Context, int64, []memory.Note, *memory.Progress) error {
	return nil
}
func (s *noteStore) LearningBatch(context.Context, int) ([]qq.Event, memory.Progress, error) {
	return nil, memory.Progress{}, nil
}
func (s *noteStore) DeferLearning(context.Context, int64, time.Duration) error { return nil }
func (s *noteStore) NotesByID(context.Context, int64, []string) ([]memory.Note, error) {
	return nil, nil
}
func (s *noteStore) NotesByIdentity(context.Context, int64, []memory.Identity) ([]memory.Note, error) {
	return nil, nil
}
func (s *noteStore) PendingNotes(context.Context, string, int) ([]memory.Note, error) {
	return nil, nil
}
func (s *noteStore) IndexNote(context.Context, memory.Note, []float32, string) error { return nil }
func (s *noteStore) MemoryRevision(context.Context, int64) (int64, error)            { return 1, nil }
func (s *noteStore) Ingest(context.Context, *qq.Event) (bool, error)                 { return false, nil }
func (s *noteStore) Recent(context.Context, int64, int) ([]qq.Event, error)          { return nil, nil }
func (s *noteStore) IsBotReply(context.Context, int64, string) (bool, error)         { return false, nil }
func (s *noteStore) BeginAttempt(context.Context, Attempt) (bool, error)             { return true, nil }
func (s *noteStore) UpdateAttempt(context.Context, Attempt) error                    { return nil }
func (s *noteStore) RepliesSince(context.Context, int64, time.Time) (int, time.Time, error) {
	return 0, time.Time{}, nil
}
func (s *noteStore) FinishAttempt(_ context.Context, _ Attempt, _ qq.Event, ids []string) error {
	s.used = append(s.used, ids...)
	return nil
}
func (s *noteStore) SaveDelivery(context.Context, string, string, string) error { return nil }
func (s *noteStore) ConfirmAction(context.Context, Attempt, ActionRecord, []string) error {
	return nil
}
func (s *noteStore) SavePendingMemory(context.Context, string, int64, []memory.NoteSnapshot) error {
	return nil
}
func (s *noteStore) DiscardPendingMemory(context.Context, string, string) error { return nil }

// TestContextCacheHitKeepsNoteIDs proves a cache hit hands the usage ledger the
// same frozen note identities as the retrieval that populated the cache.
func TestContextCacheHitKeepsNoteIDs(t *testing.T) {
	ctx := context.Background()
	store := &noteStore{notes: memory.Note{ID: "note-cache", GroupID: 1, Kind: "preference", Key: "nickname", Text: "称呼小林", State: "active", Updated: time.Now()}}
	r := New(ctx, &Runtime{Store: store, Memory: &memory.Service{Store: store, TopK: 3, Budgets: memory.RecallBudgets{Preference: 6, Member: 8, Episode: 6}}, Cache: cache.NewHub(16),
		Config: Config{History: 10, ContextRunes: 20000, Cache: CacheTTL{Context: time.Minute}}})
	t.Cleanup(r.Close)
	builder := runtimeContextBuilder{runtime: r}
	admission := ctxsnapshot.AdmissionSnapshot{GroupID: 1, Batch: []qq.Event{{ID: "e1", GroupID: 1, UserID: 10, Text: "最近怎么样"}}, Query: "最近怎么样"}
	first, err := builder.Build(ctx, admission)
	if err != nil {
		t.Fatal(err)
	}
	second, err := builder.Build(ctx, admission)
	if err != nil {
		t.Fatal(err)
	}
	if first.Retrieval.RecallCacheHit || !second.Retrieval.RecallCacheHit {
		t.Fatalf("cache states = miss:%v hit:%v", first.Retrieval.RecallCacheHit, second.Retrieval.RecallCacheHit)
	}
	for _, snapshot := range []ctxsnapshot.ContextSnapshot{first, second} {
		if !snapshot.Retrieval.RecallAttempted || snapshot.Retrieval.RecallHits != 1 {
			t.Fatalf("recall trace = %+v", snapshot.Retrieval)
		}
	}
	ids := func(snapshot ctxsnapshot.ContextSnapshot) []string { return snapshotNoteIDs(snapshot.Notes) }
	if !slices.Equal(ids(first), ids(second)) || !slices.Equal(ids(first), []string{"note-cache"}) {
		t.Fatalf("note ids miss=%v hit=%v", ids(first), ids(second))
	}
	for _, snapshot := range []ctxsnapshot.ContextSnapshot{first, second} {
		if err := store.FinishAttempt(ctx, Attempt{}, qq.Event{}, ids(snapshot)); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.used) != 2 || store.used[0] != "note-cache" || store.used[1] != "note-cache" {
		t.Fatalf("booked note ids = %v", store.used)
	}
}
func (s *noteStore) MarkReconcile(context.Context, string, string) error { return nil }
