package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
)

func TestRuntimeExecutesIdempotentTask(t *testing.T) {
	store := inmemory.NewStore()
	runtime := New(context.Background(), store, Config{WorkerCount: 1, PollInterval: time.Millisecond, TaskTimeout: time.Second, WorkerID: "test"})
	defer runtime.Close()
	var calls atomic.Int32
	if err := runtime.Register("profile", func(_ context.Context, payload []byte) error {
		var body map[string]string
		if err := json.Unmarshal(payload, &body); err != nil {
			return err
		}
		calls.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := runtime.Enqueue(context.Background(), "profile", "event-1", []byte(`{"event":"event-1"}`)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := runtime.Enqueue(context.Background(), "profile", "event-1", []byte(`{"event":"event-1"}`)); err != nil {
		t.Fatalf("duplicate enqueue: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one handler call, got %d", calls.Load())
	}
}

func TestRuntimeMovesPermanentFailureToDeadLetter(t *testing.T) {
	store := inmemory.NewStore()
	runtime := New(context.Background(), store, Config{WorkerCount: 1, PollInterval: time.Millisecond, TaskTimeout: time.Second, MaxAttempts: 1, WorkerID: "test"})
	defer runtime.Close()
	var calls atomic.Int32
	if err := runtime.Register("broken", func(context.Context, []byte) error {
		calls.Add(1)
		return errors.New("broken")
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := runtime.Enqueue(context.Background(), "broken", "event-2", []byte(`{}`)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() == 0 {
		t.Fatal("handler was not called")
	}
	for time.Now().Before(deadline) {
		if task, ok := store.LookupOutbox(TaskID("broken", "event-2")); ok && task.Status == "dead_letter" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("failed task remained claimable")
}
