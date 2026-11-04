package context_test

import (
	"testing"

	ctxsnapshot "github.com/phlin/go-agent/internal/context"
	"github.com/phlin/go-agent/internal/conversation"
	"github.com/phlin/go-agent/internal/meme"
	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/qq"
)

// The turn builder assembles its snapshot from these pure helpers, so the
// budget and freeze contract is checked here without a live store or model.
func TestSnapshotIsFrozenAndBudgeted(t *testing.T) {
	batch := make([]qq.Event, 0, 10)
	for i := 0; i < 10; i++ {
		batch = append(batch, qq.Event{ID: string(rune('a' + i)), Images: []qq.Attachment{{URL: "u"}}})
	}
	topics := []conversation.Topic{{ID: "t1", Participants: []int64{1}}, {ID: "t2"}, {ID: "t3"}, {ID: "t4"}, {ID: "t5"}}
	view := conversation.Context{Batch: []conversation.Event{{EventID: "a", TopicIDs: []string{"t1"}}}, Topics: topics}
	notes := []memory.Note{{ID: "n", Evidence: []memory.Evidence{{EventID: "e"}}}}
	assets := []meme.Asset{{ID: "s", Tags: []string{"tag"}}}

	gotBatch := ctxsnapshot.LimitEvents(batch, ctxsnapshot.MaxBatch)
	gotView := ctxsnapshot.LimitConversation(view, ctxsnapshot.MaxTopics)
	gotNotes := ctxsnapshot.CloneNotes(notes)
	gotAssets := ctxsnapshot.CloneAssets(assets)
	if len(gotBatch) != ctxsnapshot.MaxBatch || len(gotView.Topics) != ctxsnapshot.MaxTopics {
		t.Fatalf("budgets not applied: batch=%d topics=%d", len(gotBatch), len(gotView.Topics))
	}

	batch[0].Images[0].URL = "mutated"
	view.Batch[0].TopicIDs[0] = "mutated"
	view.Topics[0].Participants[0] = 99
	notes[0].Evidence[0].EventID = "mutated"
	assets[0].Tags[0] = "mutated"

	if gotBatch[0].Images[0].URL == "mutated" || gotView.Batch[0].TopicIDs[0] == "mutated" || gotView.Topics[0].Participants[0] == 99 {
		t.Fatal("input mutation leaked into the snapshot")
	}
	if gotNotes[0].Evidence[0].EventID == "mutated" || gotAssets[0].Tags[0] == "mutated" {
		t.Fatal("resource mutation leaked into the snapshot")
	}
}
