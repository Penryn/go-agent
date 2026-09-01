package ingress

import (
	"context"
	"errors"
	"sync"

	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
)

var ErrClosed = errors.New("event log is closed")

// MemoryEventLog 是去重事件账本:同一 EventID 只落一次,并分配全局递增序号。
type MemoryEventLog struct {
	mu       sync.Mutex
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

func (l *MemoryEventLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	return nil
}
