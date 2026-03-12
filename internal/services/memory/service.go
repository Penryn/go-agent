package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/phlin/go-agent/internal/core/ports"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

type WriteIntent struct {
	Scope         string
	MemoryType    string
	Subject       string
	Content       string
	SourceEventID string
	Importance    float64
	Confidence    float64
}

type Service struct {
	store ports.MemoryStore
}

func New(store ports.MemoryStore) *Service {
	return &Service{store: store}
}

func (s *Service) MarkIntent(ctx context.Context, intent WriteIntent) (memorydomain.MemoryRecord, error) {
	record := memorydomain.MemoryRecord{
		MemoryID:      fmt.Sprintf("memory-%d", time.Now().UnixNano()),
		Scope:         intent.Scope,
		Type:          intent.MemoryType,
		Subject:       intent.Subject,
		Content:       intent.Content,
		SourceEventID: intent.SourceEventID,
		Confidence:    intent.Confidence,
		Importance:    intent.Importance,
		CreatedAt:     time.Now(),
	}
	return record, s.store.UpsertMemory(ctx, record)
}

func (s *Service) Query(ctx context.Context, query ports.MemoryQuery) ([]memorydomain.MemoryRecord, error) {
	return s.store.QueryMemories(ctx, query)
}
