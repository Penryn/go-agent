package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/testsupport"
)

func TestRuntimeExecutesIdempotentTask(t *testing.T) {
	store := testsupport.NewStore(t)
	runtime := New(context.Background(), store, Config{WorkerCount: 1, PollInterval: time.Millisecond, TaskTimeout: time.Second})
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
	if err := runtime.Start(); err != nil {
		t.Fatalf("start: %v", err)
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
	store := testsupport.NewStore(t)
	runtime := New(context.Background(), store, Config{WorkerCount: 1, PollInterval: time.Millisecond, TaskTimeout: time.Second, MaxAttempts: 1})
	defer runtime.Close()
	var calls atomic.Int32
	if err := runtime.Register("broken", func(context.Context, []byte) error {
		calls.Add(1)
		return errors.New("broken")
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := runtime.Start(); err != nil {
		t.Fatalf("start: %v", err)
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
	// dead_letter 任务的 available_at 被推到无限远,不再可被 claim。
	for time.Now().Before(deadline) {
		tasks, err := store.ClaimOutbox(context.Background(), "probe-worker", time.Now(), time.Second, 10)
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if len(tasks) == 0 {
			return
		}
		for _, task := range tasks {
			if task.Kind == "broken" && task.Status == "dead_letter" {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("failed task remained claimable")
}

func TestRuntimeDoesNotClaimTasksBeforeStart(t *testing.T) {
	store := testsupport.NewStore(t)
	runtime := New(context.Background(), store, Config{WorkerCount: 1, PollInterval: time.Millisecond, TaskTimeout: time.Second})
	defer runtime.Close()
	var calls atomic.Int32
	if err := runtime.Enqueue(context.Background(), "late", "event-3", []byte(`{}`)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatalf("handler called before start: %d", calls.Load())
	}
	if err := runtime.Register("late", func(context.Context, []byte) error {
		calls.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := runtime.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := runtime.Register("too-late", func(context.Context, []byte) error { return nil }); err == nil {
		t.Fatal("expected registration after start to fail")
	}
	deadline := time.Now().Add(time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one handler call after start, got %d", calls.Load())
	}
}
