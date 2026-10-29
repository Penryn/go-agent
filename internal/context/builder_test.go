package context_test

import (
	"context"
	"testing"

	ctxsnapshot "github.com/phlin/go-agent/internal/context"
	"github.com/phlin/go-agent/internal/conversation"
	"github.com/phlin/go-agent/internal/meme"
	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/qq"
)

func TestSnapshotIsFrozenAndBudgeted(t *testing.T) {
	batch := make([]qq.Event, 0, 10)
	for i := 0; i < 10; i++ {
		batch = append(batch, qq.Event{ID: string(rune('a' + i)), Images: []qq.Attachment{{URL: "u"}}})
	}
	topics := []conversation.Topic{{ID: "t1", Participants: []int64{1}}, {ID: "t2"}, {ID: "t3"}, {ID: "t4"}, {ID: "t5"}}
	admission := ctxsnapshot.AdmissionSnapshot{
		GroupID:      1,
		Batch:        batch,
		History:      []qq.Event{{ID: "h", Images: []qq.Attachment{{URL: "h"}}}},
		Conversation: conversation.Context{Batch: []conversation.Event{{EventID: "a", TopicIDs: []string{"t1"}}}, Topics: topics},
		Query:        "query",
	}
	notes := []memory.Note{{ID: "n", Evidence: []memory.Evidence{{EventID: "e"}}}}
	assets := []meme.Asset{{ID: "s", Tags: []string{"tag"}}}
	builder := ctxsnapshot.SnapshotBuilder{
		Notes:    func(context.Context, int64, string, []float32) ([]memory.Note, error) { return notes, nil },
		Stickers: func(context.Context, int64, string, []float32) ([]meme.Asset, error) { return assets, nil },
		Images:   func(context.Context, []qq.Event) ([]string, error) { return []string{"img"}, nil },
		Embed:    func(context.Context, string) ([]float32, error) { return []float32{1}, nil },
	}
	snapshot, err := builder.Build(context.Background(), admission)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Batch) != ctxsnapshot.MaxBatch || len(snapshot.Conversation.Topics) != ctxsnapshot.MaxTopics {
		t.Fatalf("budgets not applied: batch=%d topics=%d", len(snapshot.Batch), len(snapshot.Conversation.Topics))
	}

	admission.Batch[0].Images[0].URL = "mutated"
	admission.Conversation.Batch[0].TopicIDs[0] = "mutated"
	admission.Conversation.Topics[0].Participants[0] = 99
	notes[0].Evidence[0].EventID = "mutated"
	assets[0].Tags[0] = "mutated"
	snapshot.QueryEmbedding[0] = 9
	snapshot.StickerAssets[0].Tags[0] = "caller"
	snapshot.Notes[0].Evidence[0].EventID = "caller"

	if snapshot.Batch[0].Images[0].URL == "mutated" || snapshot.Conversation.Batch[0].TopicIDs[0] == "mutated" || snapshot.Conversation.Topics[0].Participants[0] == 99 {
		t.Fatal("admission mutation leaked into the snapshot")
	}
	if snapshot.Notes[0].Evidence[0].EventID == "mutated" || snapshot.StickerAssets[0].Tags[0] == "mutated" {
		t.Fatal("resource mutation leaked into the snapshot")
	}
	if notes[0].Evidence[0].EventID == "caller" || assets[0].Tags[0] == "caller" {
		t.Fatal("caller mutation changed the builder inputs")
	}
}
