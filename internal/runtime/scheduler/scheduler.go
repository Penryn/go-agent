package scheduler

import (
	"context"
	"time"
)

type Job func(context.Context) error

type Scheduler struct {
	jobs []registeredJob
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
	for _, registered := range s.jobs {
		go func(job registeredJob) {
			ticker := time.NewTicker(job.interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					_ = job.job(ctx)
				}
			}
		}(registered)
	}
}
