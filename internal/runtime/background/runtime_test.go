package background

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeDrainsQueuedJobsOnClose(t *testing.T) {
	runtime := New(context.Background(), Config{QueueSize: 2, WorkerCount: 1})
	var completed atomic.Int32
	for i := 0; i < 2; i++ {
		if !runtime.Submit(Job{Name: "drain", Run: func(context.Context) error {
			completed.Add(1)
			return nil
		}}) {
			t.Fatal("expected job to be queued")
		}
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if completed.Load() != 2 {
		t.Fatalf("expected queued jobs to drain, completed=%d", completed.Load())
	}
	if runtime.Submit(Job{Name: "after-close", Run: func(context.Context) error { return nil }}) {
		t.Fatal("submit after close should be rejected")
	}
}

func TestRuntimeDropsWhenQueueIsFull(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	runtime := New(context.Background(), Config{QueueSize: 1, WorkerCount: 1})
	defer runtime.Close(context.Background())
	if !runtime.Submit(Job{Name: "blocking", Run: func(context.Context) error {
		close(started)
		<-release
		return nil
	}}) {
		t.Fatal("expected blocking job to be queued")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("blocking job did not start")
	}
	if !runtime.Submit(Job{Name: "queued", Run: func(context.Context) error { return nil }}) {
		t.Fatal("expected second job to fill queue")
	}
	if runtime.Submit(Job{Name: "dropped", Run: func(context.Context) error { return nil }}) {
		t.Fatal("expected full queue to drop job")
	}
	close(release)
}

func TestRuntimeAppliesJobTimeout(t *testing.T) {
	runtime := New(context.Background(), Config{QueueSize: 1, WorkerCount: 1})
	defer runtime.Close(context.Background())
	done := make(chan error, 1)
	if !runtime.Submit(Job{Name: "timeout", Timeout: 10 * time.Millisecond, Run: func(ctx context.Context) error {
		<-ctx.Done()
		done <- ctx.Err()
		return nil
	}}) {
		t.Fatal("expected timeout job to be queued")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected deadline exceeded, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for job")
	}
}
