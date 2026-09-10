package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

// service 实现 Service 接口
type service struct {
	store memorydomain.Store
	// TODO: 后续添加 retrieval service 和 evidence validator
}

// NewService 创建记忆服务实例
func NewService(store memorydomain.Store) memorydomain.Service {
	return &service{
		store: store,
	}
}

// ApplyCandidates 校验并应用记忆候选
func (s *service) ApplyCandidates(ctx context.Context, candidates []*memorydomain.MemoryCandidate) ([]*memorydomain.ApplyResult, error) {
	results := make([]*memorydomain.ApplyResult, 0, len(candidates))

	for _, candidate := range candidates {
		result := s.applyOne(ctx, candidate)
		results = append(results, result)
	}

	return results, nil
}

// applyOne 应用单个候选
func (s *service) applyOne(ctx context.Context, candidate *memorydomain.MemoryCandidate) *memorydomain.ApplyResult {
	// 1. 基本校验
	if err := memorydomain.ValidateCandidate(candidate); err != nil {
		return &memorydomain.ApplyResult{
			Candidate: candidate,
			Success:   false,
			Error:     fmt.Sprintf("validation failed: %v", err),
		}
	}

	// 2. 证据校验（TODO: 实现证据验证器）
	// 目前假设证据已经在归档的 messages 表中

	// 3. 根据意图处理
	switch candidate.Intent {
	case "new":
		return s.createNew(ctx, candidate)
	case "update", "supplement":
		return s.updateExisting(ctx, candidate)
	case "correct":
		return s.correctExisting(ctx, candidate)
	case "revoke":
		return s.revokeMemory(ctx, candidate)
	default:
		return &memorydomain.ApplyResult{
			Candidate: candidate,
			Success:   false,
			Error:     fmt.Sprintf("unknown intent: %s", candidate.Intent),
		}
	}
}

// createNew 创建新记忆
func (s *service) createNew(ctx context.Context, candidate *memorydomain.MemoryCandidate) *memorydomain.ApplyResult {
	now := time.Now()

	// 生成记忆ID
	memoryID := uuid.New().String()

	// 对于结构化事实，检查是否已存在
	if candidate.Predicate != "" {
		existing, err := s.store.ListByFactKey(ctx,
			candidate.Scope,
			candidate.SubjectKind,
			candidate.SubjectID,
			candidate.Predicate,
			candidate.Qualifier,
		)
		if err != nil {
			return &memorydomain.ApplyResult{
				Candidate: candidate,
				Success:   false,
				Error:     fmt.Sprintf("failed to check existing fact: %v", err),
			}
		}

		// 如果已存在 active 记录，这是冲突
		for _, mem := range existing {
			if mem.Status == memorydomain.MemoryStatusActive {
				return &memorydomain.ApplyResult{
					Candidate:    candidate,
					Success:      false,
					Error:        "fact key already exists",
					ConflictWith: mem.MemoryID,
				}
			}
		}
	}

	// 创建记忆
	memory := &memorydomain.Memory{
		MemoryID:         memoryID,
		Scope:            candidate.Scope,
		SubjectKind:      candidate.SubjectKind,
		SubjectID:        candidate.SubjectID,
		Type:             candidate.Type,
		Subtype:          candidate.Subtype,
		Content:          candidate.Content,
		Predicate:        candidate.Predicate,
		NormalizedValue:  candidate.NormalizedValue,
		Qualifier:        candidate.Qualifier,
		ParticipantIDs:   candidate.ParticipantIDs,
		BotRole:          candidate.BotRole,
		AnchorEventID:    candidate.AnchorEventID,
		Status:           memorydomain.MemoryStatusActive,
		Revision:         1,
		FirstObservedAt:  candidate.ObservedAt,
		LastObservedAt:   candidate.ObservedAt,
		ValidUntil:       candidate.ValidUntil,
		SourceKind:       memorydomain.SourceKindExtraction,
		ExtractorVersion: candidate.ExtractorVersion,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	// 创建证据
	evidence := make([]memorydomain.Evidence, 0, len(candidate.EvidenceEventIDs))
	for _, eventID := range candidate.EvidenceEventIDs {
		evidence = append(evidence, memorydomain.Evidence{
			MemoryID:   memoryID,
			EventID:    eventID,
			SourceRole: candidate.SourceRole,
			AddedAt:    now,
		})
	}

	// 创建变更记录
	change := &memorydomain.Change{
		ChangeID:     uuid.New().String(),
		MemoryID:     memoryID,
		ChangeKind:   "created",
		Reason:       "new memory from extraction",
		OperatorKind: "system",
		OperatorID:   candidate.ExtractorVersion,
		FromRevision: 0,
		ToRevision:   1,
		FieldDiffs:   map[string]string{},
		CreatedAt:    now,
	}

	// 事务性保存
	if err := s.store.Save(ctx, memory, evidence, change); err != nil {
		return &memorydomain.ApplyResult{
			Candidate: candidate,
			Success:   false,
			Error:     fmt.Sprintf("failed to save: %v", err),
		}
	}

	return &memorydomain.ApplyResult{
		Candidate: candidate,
		Success:   true,
		MemoryID:  memoryID,
		Status:    memorydomain.MemoryStatusActive,
	}
}

// updateExisting 更新现有记忆（补充证据）
func (s *service) updateExisting(ctx context.Context, candidate *memorydomain.MemoryCandidate) *memorydomain.ApplyResult {
	if candidate.SupersedesID == "" {
		return &memorydomain.ApplyResult{
			Candidate: candidate,
			Success:   false,
			Error:     "supersedes_id is required for update",
		}
	}

	// 获取现有记忆
	existing, err := s.store.Get(ctx, candidate.SupersedesID)
	if err != nil {
		return &memorydomain.ApplyResult{
			Candidate: candidate,
			Success:   false,
			Error:     fmt.Sprintf("failed to get existing memory: %v", err),
		}
	}

	// 只能补充 active 或 pending 状态的记忆
	if existing.Status != memorydomain.MemoryStatusActive && existing.Status != memorydomain.MemoryStatusPending {
		return &memorydomain.ApplyResult{
			Candidate: candidate,
			Success:   false,
			Error:     fmt.Sprintf("cannot update memory in status: %s", existing.Status),
		}
	}

	now := time.Now()

	// 更新记忆
	existing.LastObservedAt = candidate.ObservedAt
	existing.Revision++
	existing.UpdatedAt = now

	// 如果 pending 转为 active
	if existing.Status == memorydomain.MemoryStatusPending && candidate.Intent == "supplement" {
		existing.Status = memorydomain.MemoryStatusActive
		existing.PendingUntil = nil
	}

	// 补充证据
	evidence := make([]memorydomain.Evidence, 0, len(candidate.EvidenceEventIDs))
	for _, eventID := range candidate.EvidenceEventIDs {
		evidence = append(evidence, memorydomain.Evidence{
			MemoryID:   existing.MemoryID,
			EventID:    eventID,
			SourceRole: candidate.SourceRole,
			AddedAt:    now,
		})
	}

	// 创建变更记录
	change := &memorydomain.Change{
		ChangeID:     uuid.New().String(),
		MemoryID:     existing.MemoryID,
		ChangeKind:   "updated",
		Reason:       "evidence supplemented",
		OperatorKind: "system",
		OperatorID:   candidate.ExtractorVersion,
		FromRevision: existing.Revision - 1,
		ToRevision:   existing.Revision,
		FieldDiffs: map[string]string{
			"last_observed_at": existing.LastObservedAt.Format(time.RFC3339),
			"evidence_count":   fmt.Sprintf("+%d", len(candidate.EvidenceEventIDs)),
		},
		CreatedAt: now,
	}

	// 事务性保存
	if err := s.store.Save(ctx, existing, evidence, change); err != nil {
		return &memorydomain.ApplyResult{
			Candidate: candidate,
			Success:   false,
			Error:     fmt.Sprintf("failed to save: %v", err),
		}
	}

	return &memorydomain.ApplyResult{
		Candidate: candidate,
		Success:   true,
		MemoryID:  existing.MemoryID,
		Status:    existing.Status,
	}
}

// correctExisting 更正现有记忆（创建新版本并替代旧版本）
func (s *service) correctExisting(ctx context.Context, candidate *memorydomain.MemoryCandidate) *memorydomain.ApplyResult {
	if candidate.SupersedesID == "" {
		return &memorydomain.ApplyResult{
			Candidate: candidate,
			Success:   false,
			Error:     "supersedes_id is required for correction",
		}
	}

	// 获取现有记忆
	existing, err := s.store.Get(ctx, candidate.SupersedesID)
	if err != nil {
		return &memorydomain.ApplyResult{
			Candidate: candidate,
			Success:   false,
			Error:     fmt.Sprintf("failed to get existing memory: %v", err),
		}
	}

	// 只能更正 active 状态的记忆
	if existing.Status != memorydomain.MemoryStatusActive {
		return &memorydomain.ApplyResult{
			Candidate: candidate,
			Success:   false,
			Error:     fmt.Sprintf("cannot correct memory in status: %s", existing.Status),
		}
	}

	now := time.Now()
	newMemoryID := uuid.New().String()

	// 创建新记忆（继承部分字段）
	newMemory := &memorydomain.Memory{
		MemoryID:         newMemoryID,
		Scope:            candidate.Scope,
		SubjectKind:      candidate.SubjectKind,
		SubjectID:        candidate.SubjectID,
		Type:             candidate.Type,
		Subtype:          candidate.Subtype,
		Content:          candidate.Content,
		Predicate:        candidate.Predicate,
		NormalizedValue:  candidate.NormalizedValue,
		Qualifier:        candidate.Qualifier,
		ParticipantIDs:   candidate.ParticipantIDs,
		BotRole:          candidate.BotRole,
		AnchorEventID:    candidate.AnchorEventID,
		Status:           memorydomain.MemoryStatusActive,
		Revision:         1,
		SupersedesID:     candidate.SupersedesID,
		FirstObservedAt:  existing.FirstObservedAt, // 继承首次观察时间
		LastObservedAt:   candidate.ObservedAt,
		ValidUntil:       candidate.ValidUntil,
		SourceKind:       memorydomain.SourceKindCorrection,
		ExtractorVersion: candidate.ExtractorVersion,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	// 标记旧记忆为 superseded
	existing.Status = memorydomain.MemoryStatusSuperseded
	existing.Revision++
	existing.UpdatedAt = now

	// 新记忆的证据
	newEvidence := make([]memorydomain.Evidence, 0, len(candidate.EvidenceEventIDs))
	for _, eventID := range candidate.EvidenceEventIDs {
		newEvidence = append(newEvidence, memorydomain.Evidence{
			MemoryID:   newMemoryID,
			EventID:    eventID,
			SourceRole: candidate.SourceRole,
			AddedAt:    now,
		})
	}

	// 旧记忆的变更记录
	oldChange := &memorydomain.Change{
		ChangeID:     uuid.New().String(),
		MemoryID:     existing.MemoryID,
		ChangeKind:   "superseded",
		Reason:       fmt.Sprintf("superseded by %s", newMemoryID),
		OperatorKind: "system",
		OperatorID:   candidate.ExtractorVersion,
		FromRevision: existing.Revision - 1,
		ToRevision:   existing.Revision,
		FieldDiffs: map[string]string{
			"status": string(memorydomain.MemoryStatusSuperseded),
		},
		CreatedAt: now,
	}

	// 新记忆的变更记录
	newChange := &memorydomain.Change{
		ChangeID:     uuid.New().String(),
		MemoryID:     newMemoryID,
		ChangeKind:   "created",
		Reason:       fmt.Sprintf("correction of %s", candidate.SupersedesID),
		OperatorKind: "system",
		OperatorID:   candidate.ExtractorVersion,
		FromRevision: 0,
		ToRevision:   1,
		FieldDiffs:   map[string]string{},
		CreatedAt:    now,
	}

	// 事务性保存（需要同时更新旧记忆和创建新记忆）
	// TODO: 这里需要扩展 Store 接口支持批量操作
	if err := s.store.Save(ctx, existing, nil, oldChange); err != nil {
		return &memorydomain.ApplyResult{
			Candidate: candidate,
			Success:   false,
			Error:     fmt.Sprintf("failed to update old memory: %v", err),
		}
	}

	if err := s.store.Save(ctx, newMemory, newEvidence, newChange); err != nil {
		return &memorydomain.ApplyResult{
			Candidate: candidate,
			Success:   false,
			Error:     fmt.Sprintf("failed to save new memory: %v", err),
		}
	}

	return &memorydomain.ApplyResult{
		Candidate: candidate,
		Success:   true,
		MemoryID:  newMemoryID,
		Status:    memorydomain.MemoryStatusActive,
	}
}

// revokeMemory 撤销记忆（遗忘）
func (s *service) revokeMemory(ctx context.Context, candidate *memorydomain.MemoryCandidate) *memorydomain.ApplyResult {
	if candidate.SupersedesID == "" {
		return &memorydomain.ApplyResult{
			Candidate: candidate,
			Success:   false,
			Error:     "supersedes_id is required for revoke",
		}
	}

	// 获取现有记忆
	existing, err := s.store.Get(ctx, candidate.SupersedesID)
	if err != nil {
		return &memorydomain.ApplyResult{
			Candidate: candidate,
			Success:   false,
			Error:     fmt.Sprintf("failed to get existing memory: %v", err),
		}
	}

	// 只能撤销 active 或 pending 状态的记忆
	if existing.Status != memorydomain.MemoryStatusActive && existing.Status != memorydomain.MemoryStatusPending {
		return &memorydomain.ApplyResult{
			Candidate: candidate,
			Success:   false,
			Error:     fmt.Sprintf("cannot revoke memory in status: %s", existing.Status),
		}
	}

	now := time.Now()

	// 标记为 revoked
	existing.Status = memorydomain.MemoryStatusRevoked
	existing.Revision++
	existing.UpdatedAt = now

	// 创建变更记录
	change := &memorydomain.Change{
		ChangeID:     uuid.New().String(),
		MemoryID:     existing.MemoryID,
		ChangeKind:   "revoked",
		Reason:       "explicitly forgotten",
		OperatorKind: "user",
		OperatorID:   candidate.SubjectID, // 假设是主体本人请求遗忘
		FromRevision: existing.Revision - 1,
		ToRevision:   existing.Revision,
		FieldDiffs: map[string]string{
			"status": string(memorydomain.MemoryStatusRevoked),
		},
		CreatedAt: now,
	}

	// 事务性保存
	if err := s.store.Save(ctx, existing, nil, change); err != nil {
		return &memorydomain.ApplyResult{
			Candidate: candidate,
			Success:   false,
			Error:     fmt.Sprintf("failed to save: %v", err),
		}
	}

	// TODO: 投递异步任务清理向量索引和缓存

	return &memorydomain.ApplyResult{
		Candidate: candidate,
		Success:   true,
		MemoryID:  existing.MemoryID,
		Status:    memorydomain.MemoryStatusRevoked,
	}
}

// GetConstraints 获取行动前必须遵守的约束
func (s *service) GetConstraints(ctx context.Context, scope string, targetIDs []string) ([]memorydomain.MemoryConstraint, error) {
	constraints := make([]memorydomain.MemoryConstraint, 0)
	now := time.Now()

	for _, targetID := range targetIDs {
		// 查询该用户的所有约束类型记忆
		predicates := []memorydomain.Predicate{
			memorydomain.PredicatePreferredName,
			memorydomain.PredicateAllowPoke,
			memorydomain.PredicateAllowMention,
		}

		for _, pred := range predicates {
			memories, err := s.store.ListByFactKey(ctx, scope, memorydomain.SubjectKindUser, targetID, pred, "")
			if err != nil {
				return nil, fmt.Errorf("failed to get constraints for %s: %w", targetID, err)
			}

			for _, mem := range memories {
				// 只包含 active 且未过期的
				if mem.Status != memorydomain.MemoryStatusActive {
					continue
				}
				if mem.ValidUntil != nil && now.After(*mem.ValidUntil) {
					continue
				}

				constraints = append(constraints, memorydomain.MemoryConstraint{
					SubjectKind: mem.SubjectKind,
					SubjectID:   mem.SubjectID,
					Type:        string(mem.Predicate),
					Value:       mem.NormalizedValue,
					Qualifier:   mem.Qualifier,
					ValidUntil:  mem.ValidUntil,
				})
			}
		}
	}

	return constraints, nil
}

// ForgetMemory 明确遗忘记忆
func (s *service) ForgetMemory(ctx context.Context, memoryID string, reason string, operatorID string) error {
	existing, err := s.store.Get(ctx, memoryID)
	if err != nil {
		return fmt.Errorf("failed to get memory: %w", err)
	}

	if existing.Status == memorydomain.MemoryStatusRevoked {
		return nil // 已经撤销，幂等
	}

	now := time.Now()

	// 标记为 revoked
	existing.Status = memorydomain.MemoryStatusRevoked
	existing.Revision++
	existing.UpdatedAt = now

	// 清空敏感内容（可选，根据策略决定）
	// existing.Content = "[forgotten]"

	// 创建变更记录
	change := &memorydomain.Change{
		ChangeID:     uuid.New().String(),
		MemoryID:     existing.MemoryID,
		ChangeKind:   "revoked",
		Reason:       reason,
		OperatorKind: "user",
		OperatorID:   operatorID,
		FromRevision: existing.Revision - 1,
		ToRevision:   existing.Revision,
		FieldDiffs: map[string]string{
			"status": string(memorydomain.MemoryStatusRevoked),
		},
		CreatedAt: now,
	}

	if err := s.store.Save(ctx, existing, nil, change); err != nil {
		return fmt.Errorf("failed to save: %w", err)
	}

	// TODO: 投递异步任务清理向量索引和缓存

	return nil
}

// GetMemoryWithEvidence 获取记忆及其证据
func (s *service) GetMemoryWithEvidence(ctx context.Context, memoryID string) (*memorydomain.MemoryWithSource, error) {
	memory, err := s.store.Get(ctx, memoryID)
	if err != nil {
		return nil, err
	}

	evidence, err := s.store.GetEvidence(ctx, memoryID)
	if err != nil {
		return nil, err
	}

	eventIDs := make([]string, 0, len(evidence))
	for _, ev := range evidence {
		eventIDs = append(eventIDs, ev.EventID)
	}

	// 构建来源摘要
	sourceSummary := fmt.Sprintf("%d pieces of evidence", len(evidence))
	if memory.FirstObservedAt.Equal(memory.LastObservedAt) {
		sourceSummary += fmt.Sprintf(" from %s", memory.FirstObservedAt.Format("2006-01-02"))
	} else {
		sourceSummary += fmt.Sprintf(" from %s to %s",
			memory.FirstObservedAt.Format("2006-01-02"),
			memory.LastObservedAt.Format("2006-01-02"))
	}

	return &memorydomain.MemoryWithSource{
		Memory:           *memory,
		EvidenceCount:    len(evidence),
		EvidenceEventIDs: eventIDs,
		SourceSummary:    sourceSummary,
	}, nil
}
