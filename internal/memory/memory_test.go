package memory_test

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/cache"
	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/qq"
	"github.com/phlin/go-agent/internal/replay"
	"github.com/phlin/go-agent/internal/search/bm25"
)

type embedder struct{}

func (embedder) Embed(context.Context, string) ([]float32, error) { return []float32{1, 0, 0}, nil }
func (embedder) EmbeddingVersion() string                         { return "v1" }
func TestSemanticRecallAndPreferencesSurviveTopK(t *testing.T) {
	ctx := context.Background()
	s := replay.NewStore()
	notes := []memory.Note{{ID: "episode", GroupID: 1, Kind: "episode", Key: "game", Text: "星际探索游戏", State: "active", Updated: time.Now()}, {ID: "nickname", GroupID: 1, Subject: 42, Kind: "preference", Key: "nickname", Text: "称呼小林", State: "active", Updated: time.Now()}, {ID: "foreign", GroupID: 2, Kind: "episode", Key: "secret", Text: "另一个群的内容", State: "active", Updated: time.Now()}}
	if err := s.SaveNotes(ctx, 1, notes[:2], nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveNotes(ctx, 2, notes[2:], nil); err != nil {
		t.Fatal(err)
	}
	for _, group := range []int64{1, 2} {
		ns, _ := s.Notes(ctx, group, 10)
		for _, n := range ns {
			if err := s.IndexNote(ctx, n, []float32{1, 0, 0}, "v1"); err != nil {
				t.Fatal(err)
			}
		}
	}
	service := memory.Service{Store: s, Embedding: embedder{}, TopK: 1}
	out, trace, err := service.Recall(ctx, 1, []qq.Event{{GroupID: 1, UserID: 42, Text: "宇宙旅途"}}, "宇宙旅途")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(trace.Semantic, "episode") || len(out) < 2 {
		t.Fatal("semantic track or exact preferences were lost", out, trace)
	}
	for _, n := range out {
		if n.GroupID != 1 {
			t.Fatal("scope leak", n)
		}
	}
}
func TestExplicitTimeHintRecallsAnEpisode(t *testing.T) {
	s := replay.NewStore()
	n := memory.Note{ID: "yesterday", GroupID: 1, Kind: "episode", Text: "键盘坏了", State: "active", Updated: time.Now().AddDate(0, 0, -1)}
	if err := s.SaveNotes(context.Background(), 1, []memory.Note{n}, nil); err != nil {
		t.Fatal(err)
	}
	service := memory.Service{Store: s, TopK: 1}
	_, trace, err := service.Recall(context.Background(), 1, []qq.Event{{GroupID: 1, UserID: 42, Text: "昨天那个事情"}}, "昨天那个事情")
	if err != nil || !slices.Contains(trace.Contextual, n.ID) {
		t.Fatal(trace, err)
	}
}

type countingStore struct {
	*replay.Store
	notesCalls atomic.Int32
}

func (s *countingStore) Notes(ctx context.Context, group int64, limit int) ([]memory.Note, error) {
	s.notesCalls.Add(1)
	return s.Store.Notes(ctx, group, limit)
}

func newCachedService(store memory.Store) *memory.Service {
	return &memory.Service{
		Store:      store,
		TopK:       3,
		Cache:      cache.NewHub(64),
		IndexTTL:   time.Minute,
		IndexCache: cache.New(16, func(index *bm25.Index) *bm25.Index { return index }),
	}
}

func TestIndexAndRecallCachesReuseOneRevision(t *testing.T) {
	ctx := context.Background()
	base := replay.NewStore()
	if err := base.SaveNotes(ctx, 1, []memory.Note{{ID: "n1", GroupID: 1, Kind: "episode", Key: "game", Text: "星际探索游戏", State: "active", Updated: time.Now()}}, nil); err != nil {
		t.Fatal(err)
	}
	store := &countingStore{Store: base}
	service := newCachedService(store)
	for _, query := range []string{"星际", "探索"} {
		if _, _, err := service.RecallCached(ctx, memory.Query{GroupID: 1, Text: query}, 1, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if store.notesCalls.Load() != 1 {
		t.Fatalf("note index loads = %d, want 1", store.notesCalls.Load())
	}
	if _, _, err := service.RecallCached(ctx, memory.Query{GroupID: 1, Text: "星际"}, 1, time.Minute); err != nil {
		t.Fatal(err)
	}
	if store.notesCalls.Load() != 1 {
		t.Fatalf("recall cache missed: loads = %d", store.notesCalls.Load())
	}
	if err := base.SaveNotes(ctx, 1, []memory.Note{{ID: "n2", GroupID: 1, Kind: "episode", Key: "music", Text: "喜欢音乐", State: "active", Updated: time.Now()}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.RecallCached(ctx, memory.Query{GroupID: 1, Text: "星际"}, 1, time.Minute); err != nil {
		t.Fatal(err)
	}
	if store.notesCalls.Load() != 2 {
		t.Fatalf("revision change did not invalidate the index: loads = %d", store.notesCalls.Load())
	}
}

func TestValidityWindowAndTopicEpisodeRecall(t *testing.T) {
	ctx := context.Background()
	s := replay.NewStore()
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	notes := []memory.Note{
		{ID: "expired", GroupID: 1, Kind: "episode", Key: "old", Text: "过期经历", State: "active", Updated: time.Now(), ValidTo: &past},
		{ID: "pending", GroupID: 1, Kind: "episode", Key: "future", Text: "未来经历", State: "active", Updated: time.Now(), ValidFrom: &future},
		{ID: "topic", GroupID: 1, Kind: "episode", Key: "climb", TopicID: "topic:1:1", Text: "周末爬山", State: "active", Updated: time.Now()},
	}
	if err := s.SaveNotes(ctx, 1, notes, nil); err != nil {
		t.Fatal(err)
	}
	service := memory.Service{Store: s, TopK: 6}
	out, _, err := service.RecallCached(ctx, memory.Query{GroupID: 1, Text: "无关话题", TopicIDs: []string{"topic:1:1"}}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, note := range out {
		found[note.ID] = true
	}
	if !found["topic"] || found["expired"] || found["pending"] {
		t.Fatalf("validity or topic filter failed: %v", found)
	}
}

type failingUsageStore struct {
	*replay.Store
	fail atomic.Bool
}

func (s *failingUsageStore) FlushNoteUsage(ctx context.Context, usage map[string]int64, at time.Time) error {
	if s.fail.Load() {
		return errors.New("write unavailable")
	}
	return s.Store.FlushNoteUsage(ctx, usage, at)
}

func TestUsageFlushRetainsFailedBatch(t *testing.T) {
	ctx := context.Background()
	base := replay.NewStore()
	if err := base.SaveNotes(ctx, 1, []memory.Note{{ID: "n1", GroupID: 1, Kind: "episode", Key: "game", Text: "星际", State: "active", Updated: time.Now()}}, nil); err != nil {
		t.Fatal(err)
	}
	store := &failingUsageStore{Store: base}
	service := memory.Service{Store: store}
	service.RecordUsage([]memory.Note{{ID: "n1"}})
	store.fail.Store(true)
	if err := service.FlushUsage(ctx); err == nil {
		t.Fatal("expected the failed batch to surface")
	}
	store.fail.Store(false)
	if err := service.FlushUsage(ctx); err != nil {
		t.Fatal(err)
	}
	notes, err := base.Notes(ctx, 1, 10)
	if err != nil || notes[0].UseCount != 1 {
		t.Fatalf("usage was lost: %+v %v", notes, err)
	}
}
