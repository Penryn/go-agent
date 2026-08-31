// Package outbox executes replayable asynchronous tasks persisted in an
// OutboxStore. The queue is only a wake-up mechanism; task state lives in the
// store and survives process restarts.
package outbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/phlin/go-agent/internal/core/ports"
	"github.com/phlin/go-agent/internal/services/textutil"
)

type Handler func(context.Context, []byte) error

type Config struct {
	WorkerCount  int
	PollInterval time.Duration
	Lease        time.Duration
	TaskTimeout  time.Duration
	MaxAttempts  int
	WorkerID     string
}

func DefaultConfig() Config {
	return Config{WorkerCount: 1, PollInterval: 500 * time.Millisecond, Lease: time.Minute, TaskTimeout: 30 * time.Second, MaxAttempts: 5}
}

type Runtime struct {
	store    ports.OutboxStore
	cfg      Config
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.RWMutex
	handlers map[string]Handler
}

func New(parent context.Context, store ports.OutboxStore, cfg Config) *Runtime {
	defaults := DefaultConfig()
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = defaults.WorkerCount
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaults.PollInterval
	}
	if cfg.Lease <= 0 {
		cfg.Lease = defaults.Lease
	}
	if cfg.TaskTimeout <= 0 {
		cfg.TaskTimeout = defaults.TaskTimeout
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaults.MaxAttempts
	}
	if cfg.WorkerID == "" {
		cfg.WorkerID = fmt.Sprintf("worker-%d", time.Now().UnixNano())
	}
	ctx, cancel := context.WithCancel(parent)
	r := &Runtime{store: store, cfg: cfg, ctx: ctx, cancel: cancel, handlers: make(map[string]Handler)}
	for i := 0; i < cfg.WorkerCount; i++ {
		r.wg.Add(1)
		go r.worker(i)
	}
	return r
}

func (r *Runtime) Register(kind string, handler Handler) error {
	if r == nil || r.store == nil {
		return errors.New("outbox: runtime is not configured")
	}
	if kind == "" || handler == nil {
		return errors.New("outbox: kind and handler are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.handlers[kind]; exists {
		return fmt.Errorf("outbox: handler already registered for %q", kind)
	}
	r.handlers[kind] = handler
	return nil
}

func (r *Runtime) Enqueue(ctx context.Context, kind, idempotencyKey string, payload []byte) error {
	if r == nil || r.store == nil {
		return errors.New("outbox: runtime is not configured")
	}
	if kind == "" || idempotencyKey == "" {
		return errors.New("outbox: kind and idempotency key are required")
	}
	taskID := TaskID(kind, idempotencyKey)
	task := ports.OutboxTask{
		ID:             taskID,
		Kind:           kind,
		IdempotencyKey: idempotencyKey,
		Payload:        append([]byte(nil), payload...),
		MaxAttempts:    r.cfg.MaxAttempts,
	}
	return r.store.EnqueueOutbox(ctx, task)
}

func TaskID(kind, idempotencyKey string) string {
	hash := sha256.Sum256([]byte(kind + "\x00" + idempotencyKey))
	return kind + "-" + hex.EncodeToString(hash[:8])
}

func (r *Runtime) worker(index int) {
	defer r.wg.Done()
	workerID := fmt.Sprintf("%s-%d", r.cfg.WorkerID, index)
	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case now := <-ticker.C:
			r.processBatch(workerID, now)
		}
	}
}

func (r *Runtime) processBatch(workerID string, now time.Time) {
	if r.store == nil {
		return
	}
	tasks, err := r.store.ClaimOutbox(r.ctx, workerID, now, r.cfg.Lease, r.cfg.WorkerCount)
	if err != nil {
		slog.Warn("outbox: claim failed", "worker", workerID, "error", err)
		return
	}
	for _, task := range tasks {
		r.mu.RLock()
		handler := r.handlers[task.Kind]
		r.mu.RUnlock()
		if handler == nil {
			_ = r.store.FailOutbox(context.Background(), task.ID, fmt.Errorf("no handler registered for %q", task.Kind), time.Time{})
			continue
		}
		ctx, cancel := context.WithTimeout(r.ctx, r.cfg.TaskTimeout)
		err := handler(ctx, append([]byte(nil), task.Payload...))
		cancel()
		if err == nil {
			if completeErr := r.store.CompleteOutbox(context.Background(), task.ID); completeErr != nil {
				slog.Warn("outbox: complete failed", "task_id", task.ID, "error", completeErr)
			}
			continue
		}
		retryAt := time.Now().Add(textutil.Backoff(task.Attempts, 2*time.Second, 256*time.Second))
		if failErr := r.store.FailOutbox(context.Background(), task.ID, err, retryAt); failErr != nil {
			slog.Warn("outbox: fail update failed", "task_id", task.ID, "error", failErr)
		}
	}
}

func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.cancel()
	r.wg.Wait()
	return nil
}
