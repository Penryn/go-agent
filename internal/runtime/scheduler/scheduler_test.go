package scheduler

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSchedulerRunsJob(t *testing.T) {
	s := New()
	var count atomic.Int32
	s.Register("tick", 20*time.Millisecond, func(context.Context) error {
		count.Add(1)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	s.Start(ctx)
	<-ctx.Done()

	if count.Load() == 0 {
		t.Fatalf("expected scheduled job to run")
	}
}

func TestSchedulerCloseStopsJobs(t *testing.T) {
	s := New()
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	s.Register("tick", time.Millisecond, func(context.Context) error {
		startOnce.Do(func() { close(started) })
		<-release
		return nil
	})
	s.Start(context.Background())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("expected scheduled job to run")
	}
	closed := make(chan error, 1)
	go func() { closed <- s.Close(context.Background()) }()
	select {
	case err := <-closed:
		t.Fatalf("close returned before callback finished: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not wait for callback")
	}
}
