package review

import (
	"context"
	"crypto/sha256"
	"fmt"

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
		// 构造幂等 MemoryID：同一群+类型+值 三元组始终映射到同一 ID，重复学习时覆盖而非新增。
		raw := fmt.Sprintf("learning-%d-%s-%s", candidate.GroupID, candidate.Kind, candidate.Value)
		sum := sha256.Sum256([]byte(raw))
		memID := fmt.Sprintf("memory-%x", sum[:8])

		// 根据 TargetUserID 决定 Scope：0 为群级别，非零为用户级别。
		scope := fmt.Sprintf("group:%d", candidate.GroupID)
		if candidate.TargetUserID != 0 {
			scope = fmt.Sprintf("group:%d:user:%d", candidate.GroupID, candidate.TargetUserID)
		}

		evidenceEventID := ""
		if len(candidate.ExampleEventIDs) > 0 {
			evidenceEventID = candidate.ExampleEventIDs[0]
		}
		if _, err := s.memory.MarkIntent(ctx, memsvc.WriteIntent{
			MemoryID:      memID,
			Scope:         scope,
			MemoryType:    candidate.Kind,
			Subject:       candidate.Value,
			Content:       candidate.Meaning,
			SourceEventID: evidenceEventID,
			Importance:    float64(candidate.EvidenceCount) / 20,
			Confidence:    candidate.Confidence,
		}); err != nil {
			return err
		}
	}
	return nil
}
