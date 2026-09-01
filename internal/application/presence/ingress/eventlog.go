package ingress

import (
	"context"
	"errors"
	"sync"

	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
)

var ErrClosed = errors.New("event log is closed")

type EventLog interface {
	Append(context.Context, presencedomain.EventRecord) error
	Recent(context.Context, int64, int) ([]presencedomain.EventRecord, error)
}

type DeduplicatingEventLog interface {
	EventLog
	AppendIfNew(context.Context, presencedomain.EventRecord) (bool, error)
}

// MemoryEventLog is the first EventLog adapter. Its interface deliberately
// keeps persistence replaceable; a durable adapter can be added without
// changing the Group Actor or ingress caller.
type MemoryEventLog struct {
	mu       sync.RWMutex
	closed   bool
	sequence uint64
	byGroup  map[int64][]presencedomain.EventRecord
	seen     map[string]struct{}
}

func NewMemoryEventLog() *MemoryEventLog {
	return &MemoryEventLog{
		byGroup: make(map[int64][]presencedomain.EventRecord),
		seen:    make(map[string]struct{}),
	}
}

func (l *MemoryEventLog) Append(ctx context.Context, record presencedomain.EventRecord) error {
	_, err := l.AppendIfNew(ctx, record)
	return err
}

func (l *MemoryEventLog) AppendIfNew(_ context.Context, record presencedomain.EventRecord) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return false, ErrClosed
	}
	if record.EventID == "" {
		return false, errors.New("event log: event id is required")
	}
	if _, ok := l.seen[record.EventID]; ok {
		return false, nil
	}
	l.sequence++
	record.Sequence = l.sequence
	l.seen[record.EventID] = struct{}{}
	record.RawPayload = append([]byte(nil), record.RawPayload...)
	l.byGroup[record.GroupID] = append(l.byGroup[record.GroupID], record)
	return true, nil
}

func (l *MemoryEventLog) Recent(_ context.Context, groupID int64, limit int) ([]presencedomain.EventRecord, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	items := l.byGroup[groupID]
	if limit > 0 && len(items) > limit {
		items = items[len(items)-limit:]
	}
	result := make([]presencedomain.EventRecord, len(items))
	copy(result, items)
	for i := range result {
		result[i].RawPayload = append([]byte(nil), result[i].RawPayload...)
	}
	return result, nil
}

func (l *MemoryEventLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	return nil
}
