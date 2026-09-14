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

	"github.com/phlin/go-agent/internal/application/ports"
	"github.com/phlin/go-agent/internal/application/textutil"
)

type Handler func(context.Context, []byte) error

type Config struct {
	WorkerCount  int
	PollInterval time.Duration
	TaskTimeout  time.Duration
	MaxAttempts  int
}

func DefaultConfig() Config {
	return Config{WorkerCount: 1, PollInterval: 500 * time.Millisecond, TaskTimeout: 30 * time.Second, MaxAttempts: 5}
}

// workerLease 是任务认领后的租期;单进程部署下足够,多实例部署时按
// TaskTimeout 同步放宽即可。workerID 进程内唯一即可标识认领方。
const workerLease = time.Minute

type Runtime struct {
	store    ports.OutboxStore
	cfg      Config
	workerID string
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.RWMutex
	handlers map[string]Handler
	started  bool
	closed   bool
}

func New(parent context.Context, store ports.OutboxStore, cfg Config) *Runtime {
	defaults := DefaultConfig()
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = defaults.WorkerCount
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaults.PollInterval
	}
	if cfg.TaskTimeout <= 0 {
		cfg.TaskTimeout = defaults.TaskTimeout
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaults.MaxAttempts
	}
	workerID := fmt.Sprintf("worker-%d", time.Now().UnixNano())
	ctx, cancel := context.WithCancel(parent)
	return &Runtime{store: store, cfg: cfg, workerID: workerID, ctx: ctx, cancel: cancel, handlers: make(map[string]Handler)}
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
	if r.closed {
		return errors.New("outbox: runtime is closed")
	}
	if r.started {
		return errors.New("outbox: handlers must be registered before start")
	}
	if _, exists := r.handlers[kind]; exists {
		return fmt.Errorf("outbox: handler already registered for %q", kind)
	}
	r.handlers[kind] = handler
	return nil
}

// Start seals the handler registry and starts task consumption. Keeping
// construction separate from startup prevents persisted tasks from being
// claimed before App has registered every handler.
func (r *Runtime) Start() error {
	if r == nil || r.store == nil {
		return errors.New("outbox: runtime is not configured")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("outbox: runtime is closed")
	}
	if r.started {
		return nil
	}
	r.started = true
	for i := 0; i < r.cfg.WorkerCount; i++ {
		r.wg.Add(1)
		go r.worker(i)
	}
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
	workerID := fmt.Sprintf("%s-%d", r.workerID, index)
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
	tasks, err := r.store.ClaimOutbox(r.ctx, workerID, now, workerLease, r.cfg.WorkerCount)
	if err != nil {
		slog.Warn("outbox: claim failed", "worker", workerID, "error", err)
		return
	}
	for _, task := range tasks {
		r.mu.RLock()
		handler := r.handlers[task.Kind]
		r.mu.RUnlock()
		if handler == nil {
			retryAt := time.Now().Add(textutil.Backoff(task.Attempts, 2*time.Second, 256*time.Second))
			ctx, cancel := r.finalizeContext()
			_ = r.store.FailOutbox(ctx, task.ID, fmt.Errorf("no handler registered for %q", task.Kind), retryAt)
			cancel()
			continue
		}
		ctx, cancel := context.WithTimeout(r.ctx, r.cfg.TaskTimeout)
		err := handler(ctx, append([]byte(nil), task.Payload...))
		cancel()
		if err == nil {
			finalizeCtx, finalizeCancel := r.finalizeContext()
			completeErr := r.store.CompleteOutbox(finalizeCtx, task.ID)
			finalizeCancel()
			if completeErr != nil {
				slog.Warn("outbox: complete failed", "task_id", task.ID, "error", completeErr)
			}
			continue
		}
		retryAt := time.Now().Add(textutil.Backoff(task.Attempts, 2*time.Second, 256*time.Second))
		finalizeCtx, finalizeCancel := r.finalizeContext()
		failErr := r.store.FailOutbox(finalizeCtx, task.ID, err, retryAt)
		finalizeCancel()
		if failErr != nil {
			slog.Warn("outbox: fail update failed", "task_id", task.ID, "error", failErr)
		}
	}
}

func (r *Runtime) finalizeContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.ctx), 5*time.Second)
}

func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.mu.Unlock()
	r.cancel()
	r.wg.Wait()
	return nil
}
