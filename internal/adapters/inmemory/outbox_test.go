package inmemory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/core/ports"
)

func TestOutboxIsIdempotentAndRetriesBeforeDeadLetter(t *testing.T) {
	store := NewStore()
	ctx := context.Background()
	if err := store.EnqueueOutbox(ctx, ports.OutboxTask{Kind: "media", IdempotencyKey: "event-1", Payload: []byte(`{"event":"event-1"}`), MaxAttempts: 2}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := store.EnqueueOutbox(ctx, ports.OutboxTask{Kind: "media", IdempotencyKey: "event-1"}); err != nil {
		t.Fatalf("duplicate enqueue: %v", err)
	}
	now := time.Now()
	claimed, err := store.ClaimOutbox(ctx, "worker-a", now, time.Minute, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: tasks=%+v err=%v", claimed, err)
	}
	if claimed[0].Attempts != 1 || claimed[0].Status != ports.OutboxRunning {
		t.Fatalf("unexpected first claim: %+v", claimed[0])
	}
	if err := store.FailOutbox(ctx, claimed[0].ID, errors.New("temporary"), now.Add(time.Second)); err != nil {
		t.Fatalf("retry: %v", err)
	}
	claimed, err = store.ClaimOutbox(ctx, "worker-b", now.Add(2*time.Second), time.Minute, 1)
	if err != nil || len(claimed) != 1 || claimed[0].Attempts != 2 {
		t.Fatalf("claim retry: tasks=%+v err=%v", claimed, err)
	}
	if err := store.FailOutbox(ctx, claimed[0].ID, errors.New("permanent"), now.Add(3*time.Second)); err != nil {
		t.Fatalf("dead letter: %v", err)
	}
	if claimed, err := store.ClaimOutbox(ctx, "worker-c", now.Add(time.Hour), time.Minute, 1); err != nil || len(claimed) != 0 {
		t.Fatalf("dead letter should not be claimable: tasks=%+v err=%v", claimed, err)
	}
}
