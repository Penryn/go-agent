package inmemory

import (
	"context"
	"testing"
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
)

func TestRecentEventsUsesChronologicalCursorOrder(t *testing.T) {
	store := NewStore()
	ctx := context.Background()
	for _, event := range []conversationdomain.ConversationEvent{
		{EventID: "late", GroupID: 1, TimestampUnix: 30},
		{EventID: "same-b", GroupID: 1, TimestampUnix: 20},
		{EventID: "early", GroupID: 1, TimestampUnix: 10},
		{EventID: "same-a", GroupID: 1, TimestampUnix: 20},
	} {
		if err := store.ArchiveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	events, err := store.EventsAfter(ctx, 1, time.Unix(10, 0), "early", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].EventID != "same-a" || events[1].EventID != "same-b" || events[2].EventID != "late" {
		t.Fatalf("unexpected cursor order: %+v", events)
	}
}
