package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type Job func(context.Context) error

type Scheduler struct {
	jobs   []registeredJob
	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type registeredJob struct {
	name     string
	interval time.Duration
	job      Job
}

func New() *Scheduler {
	return &Scheduler{}
}

func (s *Scheduler) Register(name string, interval time.Duration, job Job) {
	s.jobs = append(s.jobs, registeredJob{name: name, interval: interval, job: job})
}

func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return
	}
	jobCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	jobs := append([]registeredJob(nil), s.jobs...)
	s.mu.Unlock()
	for _, registered := range jobs {
		if registered.interval <= 0 || registered.job == nil {
			continue
		}
		s.wg.Add(1)
		go func(job registeredJob) {
			defer s.wg.Done()
			ticker := time.NewTicker(job.interval)
			defer ticker.Stop()
			for {
				select {
				case <-jobCtx.Done():
					return
				case <-ticker.C:
					if err := job.job(jobCtx); err != nil {
						slog.Error("scheduler: job failed", "job", job.name, "error", err)
					}
				}
			}
		}(registered)
	}
}

// Close cancels all periodic jobs and waits for callbacks to return.
func (s *Scheduler) Close(ctx context.Context) error {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
