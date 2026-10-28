package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/phlin/go-agent/internal/model"
)

// runBackground owns bounded, cancellable process maintenance. Individual
// services decide what a tick does; no chat turn is persisted as a job.
func runBackground(ctx context.Context, wg *sync.WaitGroup, interval time.Duration, tick func(context.Context) error) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		timer := time.NewTicker(interval)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				call, cancel := context.WithTimeout(model.Background(ctx), 30*time.Second)
				err := tick(call)
				cancel()
				if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, model.ErrUnavailable) {
					slog.Warn("background maintenance", "error", err)
				}
			}
		}
	}()
}
