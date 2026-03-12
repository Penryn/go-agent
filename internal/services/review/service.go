package review

import (
	"context"

	"github.com/phlin/go-agent/internal/domain/memory"
	memsvc "github.com/phlin/go-agent/internal/services/memory"
)

type Service struct {
	memory *memsvc.Service
}

func New(memory *memsvc.Service) *Service {
	return &Service{memory: memory}
}

func (s *Service) ApplyCurator(ctx context.Context, intents []memsvc.WriteIntent) error {
	for _, intent := range intents {
		if intent.Confidence < 0.7 {
			continue
		}
		if _, err := s.memory.MarkIntent(ctx, intent); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ApplyLearning(ctx context.Context, candidates []memory.LearningCandidate) error {
	for _, candidate := range candidates {
		if candidate.Confidence < 0.7 {
			continue
		}
		if _, err := s.memory.MarkIntent(ctx, memsvc.WriteIntent{
			Scope:      "group_learning",
			MemoryType: candidate.Kind,
			Subject:    candidate.Value,
			Content:    candidate.Meaning,
			Importance: candidate.Confidence,
			Confidence: candidate.Confidence,
		}); err != nil {
			return err
		}
	}
	return nil
}
