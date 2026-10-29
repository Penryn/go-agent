package memory_test

import (
	"context"
	"fmt"
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
		ns, _ := s.ActiveNotes(ctx, group, 10)
		for _, n := range ns {
			if err := s.IndexNote(ctx, n, []float32{1, 0, 0}, "v1"); err != nil {
				t.Fatal(err)
			}
		}
	}
	service := memory.Service{Store: s, Embedding: embedder{}, TopK: 1, Budgets: memory.RecallBudgets{Preference: 6, Member: 8, Episode: 6}}
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
	service := memory.Service{Store: s, TopK: 1, Budgets: memory.RecallBudgets{Preference: 6, Member: 8, Episode: 6}}
	_, trace, err := service.Recall(context.Background(), 1, []qq.Event{{GroupID: 1, UserID: 42, Text: "昨天那个事情"}}, "昨天那个事情")
	if err != nil || !slices.Contains(trace.Contextual, n.ID) {
		t.Fatal(trace, err)
	}
}

type countingStore struct {
	*replay.Store
	notesCalls atomic.Int32
}

func (s *countingStore) ActiveNotes(ctx context.Context, group int64, limit int) ([]memory.Note, error) {
	s.notesCalls.Add(1)
	return s.Store.ActiveNotes(ctx, group, limit)
}

func newCachedService(store memory.Store) *memory.Service {
	return &memory.Service{
		Store:      store,
		TopK:       3,
		Budgets:    memory.RecallBudgets{Preference: 6, Member: 8, Episode: 6},
		Cache:      cache.NewHub(64),
		IndexTTL:   time.Minute,
		IndexCache: cache.New(16, func(index *bm25.Index) *bm25.Index { return index }),
	}
}

func TestZeroRecallBudgetDisablesCategory(t *testing.T) {
	ctx := context.Background()
	s := replay.NewStore()
	notes := []memory.Note{
		{ID: "pref", GroupID: 1, Subject: 0, Kind: "preference", Key: "番茄", Text: "偏好记忆番茄", State: "active", Updated: time.Now()},
		{ID: "member", GroupID: 1, Subject: 10, Kind: "member", Key: "番茄", Text: "成员记忆番茄", State: "active", Updated: time.Now()},
		{ID: "episode", GroupID: 1, Subject: 10, Kind: "episode", TopicID: "topic:1", Key: "番茄", Text: "经历记忆番茄", State: "active", Updated: time.Now()},
	}
	if err := s.SaveNotes(ctx, 1, notes, nil); err != nil {
		t.Fatal(err)
	}
	query := memory.Query{GroupID: 1, Events: []qq.Event{{ID: "eval", GroupID: 1, UserID: 10, Text: "番茄"}}, Text: "番茄", TopicIDs: []string{"topic:1"}}
	disabled := memory.Service{Store: s, TopK: 5}
	out, _, err := disabled.RecallCached(ctx, query, 0)
	if err != nil || len(out) != 0 {
		t.Fatalf("zero budgets recalled %+v err=%v", out, err)
	}
	enabled := memory.Service{Store: s, TopK: 5, Budgets: memory.RecallBudgets{Preference: 1, Member: 1, Episode: 1}}
	out, _, err = enabled.RecallCached(ctx, query, 0)
	if err != nil || len(out) != 3 {
		t.Fatalf("enabled budgets recalled %+v err=%v", out, err)
	}
}

func TestRecallBudgetsAreFinalCaps(t *testing.T) {
	ctx := context.Background()
	makeStore := func(withProfiles bool) *replay.Store {
		s := replay.NewStore()
		notes := []memory.Note{}
		for i := 0; i < 8; i++ {
			notes = append(notes, memory.Note{ID: fmt.Sprintf("p%d", i), GroupID: 1, Subject: 0, Kind: "preference", Key: fmt.Sprintf("p%d", i), Text: "番茄偏好", State: "active", Updated: time.Now()})
			if withProfiles {
				notes = append(notes,
					memory.Note{ID: fmt.Sprintf("m%d", i), GroupID: 1, Subject: 10, Kind: "member", Key: fmt.Sprintf("m%d", i), Text: "番茄成员", State: "active", Updated: time.Now()},
					memory.Note{ID: fmt.Sprintf("e%d", i), GroupID: 1, Subject: 10, Kind: "episode", TopicID: "topic:1", Key: fmt.Sprintf("e%d", i), Text: "番茄经历", State: "active", Updated: time.Now()})
			}
		}
		if err := s.SaveNotes(ctx, 1, notes, nil); err != nil {
			t.Fatal(err)
		}
		return s
	}
	query := memory.Query{GroupID: 1, Events: []qq.Event{{ID: "eval", GroupID: 1, UserID: 10, Text: "番茄"}}, Text: "番茄", TopicIDs: []string{"topic:1"}}
	counts := func(notes []memory.Note) map[string]int {
		out := map[string]int{}
		for _, n := range notes {
			out[n.Kind]++
		}
		return out
	}

	onlyPreferences := memory.Service{Store: makeStore(false), TopK: 5, Budgets: memory.RecallBudgets{Preference: 1, Member: 8, Episode: 6}}
	out, _, err := onlyPreferences.RecallCached(ctx, query, 0)
	if err != nil || len(out) != 1 || counts(out)["preference"] != 1 {
		t.Fatalf("preference cap = %+v err=%v", out, err)
	}
	mixed := memory.Service{Store: makeStore(true), TopK: 5, Budgets: memory.RecallBudgets{Preference: 1, Member: 1, Episode: 1}}
	out, _, err = mixed.RecallCached(ctx, query, 0)
	got := counts(out)
	if err != nil || len(out) != 3 || got["preference"] != 1 || got["member"] != 1 || got["episode"] != 1 {
		t.Fatalf("category caps = %+v err=%v", out, err)
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
	for i, query := range []string{"星际", "探索"} {
		q := memory.Query{GroupID: 1, Text: query, Events: []qq.Event{{ID: fmt.Sprint("event-", i), UserID: 10}}}
		if _, _, err := service.RecallCached(ctx, q, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if store.notesCalls.Load() != 1 {
		t.Fatalf("note index loads = %d, want 1", store.notesCalls.Load())
	}
	// A new event with the same visible participants and topics must reuse the
	// cached result even though the actor version has advanced.
	if _, _, err := service.RecallCached(ctx, memory.Query{GroupID: 1, Text: "星际", Events: []qq.Event{{ID: "later-event", UserID: 10}}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if store.notesCalls.Load() != 1 {
		t.Fatalf("recall cache missed: loads = %d", store.notesCalls.Load())
	}
	if err := base.SaveNotes(ctx, 1, []memory.Note{{ID: "n2", GroupID: 1, Kind: "episode", Key: "music", Text: "喜欢音乐", State: "active", Updated: time.Now()}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.RecallCached(ctx, memory.Query{GroupID: 1, Text: "星际", Events: []qq.Event{{ID: "after-change", UserID: 10}}}, time.Minute); err != nil {
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
	service := memory.Service{Store: s, TopK: 6, Budgets: memory.RecallBudgets{Preference: 6, Member: 8, Episode: 6}}
	out, _, err := service.RecallCached(ctx, memory.Query{GroupID: 1, Text: "无关话题", TopicIDs: []string{"topic:1:1"}}, 0)
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
