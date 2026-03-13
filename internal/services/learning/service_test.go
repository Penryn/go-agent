package learning

import (
	"context"
	"testing"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	memsvc "github.com/phlin/go-agent/internal/services/memory"
	reviewsvc "github.com/phlin/go-agent/internal/services/review"
)

func TestRun(t *testing.T) {
	store := inmemory.NewStore()
	memorySvc := memsvc.New(store)
	reviewService := reviewsvc.New(memorySvc)
	service, err := New(context.Background(), store, reviewService)
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
