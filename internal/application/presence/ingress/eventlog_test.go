package ingress

import (
	"context"
	"testing"
	"time"

	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
)

func TestMemoryEventLogDeduplicatesAndAssignsSequence(t *testing.T) {
	log := NewMemoryEventLog()
	now := time.Unix(100, 0)
	record := presencedomain.EventRecord{EventID: "e1", GroupID: 1, Timestamp: now}
	if first, err := log.AppendIfNew(context.Background(), record); err != nil || !first {
		t.Fatalf("append first event: first=%v err=%v", first, err)
	}
	if dup, err := log.AppendIfNew(context.Background(), record); err != nil || dup {
		t.Fatalf("duplicate event should be dropped: dup=%v err=%v", dup, err)
	}
	if ok, err := log.AppendIfNew(context.Background(), presencedomain.EventRecord{EventID: "e2", GroupID: 1, Timestamp: now.Add(time.Second)}); err != nil || !ok {
		t.Fatalf("append second event: ok=%v err=%v", ok, err)
	}
	if ok, err := log.AppendIfNew(context.Background(), presencedomain.EventRecord{}); err == nil || ok {
		t.Fatal("empty event id should be rejected")
	}
}
