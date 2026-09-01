package learning

import (
	"context"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	memsvc "github.com/phlin/go-agent/internal/application/memory"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
)

func TestRun(t *testing.T) {
	store := inmemory.NewStore()
	service, err := New(context.Background(), store, store, memsvc.New(store))
	if err != nil {
		t.Fatalf("new learning service: %v", err)
	}

	output, err := service.Run(context.Background(), Input{
		GroupID: 1,
		Events: []conversationdomain.ConversationEvent{
			{Text: "离谱", UserID: 1},
			{Text: "离谱", UserID: 2},
			{Text: "离谱", UserID: 1},
			{Text: "今天正常", UserID: 3},
		},
	})
	if err != nil {
		t.Fatalf("run learning service: %v", err)
	}
	if len(output.Candidates) == 0 || output.Candidates[0].Value != "离谱" {
		t.Fatalf("unexpected candidates: %#v", output.Candidates)
	}
}

func TestLearnGroupAdvancesDurableWatermark(t *testing.T) {
	ctx := context.Background()
	store := inmemory.NewStore()
	service, err := New(ctx, store, store, memsvc.New(store))
	if err != nil {
		t.Fatalf("new learning service: %v", err)
	}

	for i := 0; i < 10; i++ {
		event := conversationdomain.ConversationEvent{
			EventID:       "event-" + string(rune('a'+i)),
			GroupID:       1,
			UserID:        int64(i % 3),
			MessageID:     "message-" + string(rune('a'+i)),
			Text:          "离谱",
			TimestampUnix: time.Unix(100+int64(i), 0).Unix(),
		}
		if err := store.ArchiveEvent(ctx, event); err != nil {
			t.Fatalf("archive event: %v", err)
		}
	}

	if err := service.learnGroup(ctx, 1); err != nil {
		t.Fatalf("learn group: %v", err)
	}
	watermark, err := store.GetLearningWatermark(ctx, 1, "learning_extract")
	if err != nil {
		t.Fatalf("get watermark: %v", err)
	}
	if watermark.EventID != "event-j" {
		t.Fatalf("watermark event = %q, want event-j", watermark.EventID)
	}
	if err := service.learnGroup(ctx, 1); err != nil {
		t.Fatalf("relearn group: %v", err)
	}
	next, err := store.GetLearningWatermark(ctx, 1, "learning_extract")
	if err != nil {
		t.Fatalf("get second watermark: %v", err)
	}
	if next.EventID != watermark.EventID || !next.OccurredAt.Equal(watermark.OccurredAt) {
		t.Fatalf("watermark changed without new facts: before=%+v after=%+v", watermark, next)
	}
}
