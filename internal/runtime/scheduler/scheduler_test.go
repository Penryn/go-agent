package scheduler

import (
	"context"
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
