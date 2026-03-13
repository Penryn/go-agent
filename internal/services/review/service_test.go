package review

import (
	"context"
	"testing"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	"github.com/phlin/go-agent/internal/core/ports"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	memsvc "github.com/phlin/go-agent/internal/services/memory"
)

func TestApplyLearningWritesMemory(t *testing.T) {
	store := inmemory.NewStore()
	service := New(memsvc.New(store))

	err := service.ApplyLearning(context.Background(), []memorydomain.LearningCandidate{
		{
			Kind:       "group_slang",
			Value:      "离谱",
			Meaning:    "群里高频复用的表达",
			Confidence: 0.9,
		},
	})
	if err != nil {
		t.Fatalf("apply learning: %v", err)
	}

	records, err := memsvc.New(store).Query(context.Background(), ports.MemoryQuery{
		Scope: "group:0",
		Query: "离谱",
		TopK:  3,
	})
	if err != nil {
		t.Fatalf("query memory: %v", err)
	}
	if len(records) == 0 {
		t.Fatalf("expected approved learning memory")
	}
}
