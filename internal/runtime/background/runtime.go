// Package background owns process-local asynchronous work and its lifecycle.
package background

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

var ErrClosed = errors.New("background runtime is closed")

type Config struct {
	QueueSize   int
	WorkerCount int
}

func DefaultConfig() Config {
	return Config{QueueSize: 128, WorkerCount: 1}
}

type Job struct {
	Name    string
	Timeout time.Duration
	Run     func(context.Context) error
}

type Runtime struct {
	ctx    context.Context
	cancel context.CancelFunc
	jobs   chan Job
	wg     sync.WaitGroup
	mu     sync.Mutex
	closed bool
}

func New(parent context.Context, cfg Config) *Runtime {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = DefaultConfig().QueueSize
	}
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = DefaultConfig().WorkerCount
	}
	ctx, cancel := context.WithCancel(parent)
	runtime := &Runtime{ctx: ctx, cancel: cancel, jobs: make(chan Job, cfg.QueueSize)}
	for i := 0; i < cfg.WorkerCount; i++ {
		runtime.wg.Add(1)
		go runtime.worker()
	}
	return runtime
}

// Submit queues a job without blocking. It returns false when the queue is
// full, the runtime is closed, or the parent context has been canceled.
func (r *Runtime) Submit(job Job) bool {
	if job.Run == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	select {
	case <-r.ctx.Done():
		return false
	case r.jobs <- job:
		return true
	default:
		slog.Warn("background: queue full, job dropped", "job", job.Name)
		return false
	}
}

func (r *Runtime) worker() {
	defer r.wg.Done()
	for {
		select {
		case <-r.ctx.Done():
			return
		case job, ok := <-r.jobs:
			if !ok {
				return
			}
			runJob(r.ctx, job)
		}
	}
}

func runJob(parent context.Context, job Job) {
	ctx := parent
	cancel := func() {}
	if job.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, job.Timeout)
	}
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error("background: job panic", "job", job.Name, "panic", recovered)
		}
	}()
	if err := job.Run(ctx); err != nil {
		slog.Warn("background: job failed", "job", job.Name, "error", err)
	}
}

// RunInline executes a job with the same timeout and recovery semantics. It is
// used by compatibility callers that have not been wired to a Runtime yet.
func RunInline(ctx context.Context, job Job) {
	runJob(ctx, job)
}

// Close stops accepting jobs, drains queued work, and waits for workers.
func (r *Runtime) Close(ctx context.Context) error {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.jobs)
	}
	r.mu.Unlock()

	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		r.cancel()
		return nil
	case <-ctx.Done():
		r.cancel()
		return ctx.Err()
	}
}
