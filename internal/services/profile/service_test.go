package profile

import (
	"context"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
)

func TestObserveEventUpdatesProfile(t *testing.T) {
	store := inmemory.NewStore()
	service := New(store)

	err := service.ObserveEvent(context.Background(), conversationdomain.ConversationEvent{
		GroupID:       1,
		UserID:        2,
		Text:          "离谱",
		TimestampUnix: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("observe event: %v", err)
	}

	profile, err := service.Query(context.Background(), 1, 2)
	if err != nil {
		t.Fatalf("query profile: %v", err)
	}
	if profile.Stats.MessageCount != 1 {
		t.Fatalf("unexpected message count: %d", profile.Stats.MessageCount)
	}
	if len(profile.CommonPhrases) == 0 {
		t.Fatalf("expected common phrase")
	}
}
