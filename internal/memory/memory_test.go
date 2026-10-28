package memory_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/qq"
	"github.com/phlin/go-agent/internal/replay"
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
