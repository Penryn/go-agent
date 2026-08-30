package ingress

import (
	"context"
	"testing"
	"time"

	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
)

func TestMemoryEventLogDeduplicatesAndAssignsSequence(t *testing.T) {
	log := NewMemoryEventLog()
	now := time.Unix(100, 0)
	record := humandomain.EventRecord{EventID: "e1", GroupID: 1, Timestamp: now}
	if err := log.Append(context.Background(), record); err != nil {
		t.Fatalf("append first event: %v", err)
	}
	if err := log.Append(context.Background(), record); err != nil {
		t.Fatalf("append duplicate event: %v", err)
	}
	if err := log.Append(context.Background(), humandomain.EventRecord{EventID: "e2", GroupID: 1, Timestamp: now.Add(time.Second)}); err != nil {
		t.Fatalf("append second event: %v", err)
	}

	items, err := log.Recent(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if len(items) != 2 || items[0].Sequence != 1 || items[1].Sequence != 2 {
		t.Fatalf("unexpected records: %+v", items)
	}
}
