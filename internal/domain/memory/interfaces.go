package memory

import (
	"context"
	"fmt"
	"time"
)

// Store 定义记忆存储接口
type Store interface {
	// Save 保存记忆、证据和变更记录（事务性）
	Save(ctx context.Context, mem *Memory, evidence []Evidence, change *Change) error

	// Get 获取单条记忆
	Get(ctx context.Context, memoryID string) (*Memory, error)

	// ListBySubject 按主体查询记忆
	ListBySubject(ctx context.Context, scope string, subjectKind SubjectKind, subjectID string, statuses []MemoryStatus) ([]*Memory, error)

	// ListByFactKey 精确查询结构化事实
	ListByFactKey(ctx context.Context, scope string, subjectKind SubjectKind, subjectID string, predicate Predicate, qualifier string) ([]*Memory, error)

	// ListByParticipant 按参与者查询情景记忆
	ListByParticipant(ctx context.Context, scope string, participantID string, statuses []MemoryStatus) ([]*Memory, error)

	// GetEvidence 获取记忆的证据
	GetEvidence(ctx context.Context, memoryID string) ([]Evidence, error)

	// GetChanges 获取记忆的变更历史
	GetChanges(ctx context.Context, memoryID string, limit int) ([]*Change, error)

	// LockFactKey 锁定事实键（用于并发控制）
	LockFactKey(ctx context.Context, scope string, subjectKind SubjectKind, subjectID string, predicate Predicate, qualifier string) error

	// MarkProgress 标记学习任务完成
	MarkProgress(ctx context.Context, progress *LearningEventProgress) error

	// GetProgress 获取事件处理进度
	GetProgress(ctx context.Context, eventID string, extractorVersion string) (*LearningEventProgress, error)

	// ListUnprocessedEvents 查询未处理的事件
	ListUnprocessedEvents(ctx context.Context, groupID int64, extractorVersion string, limit int) ([]string, error)
}

// Service 定义记忆服务（唯一写入方）
type Service interface {
	// ApplyCandidates 校验并应用记忆候选
	ApplyCandidates(ctx context.Context, candidates []*MemoryCandidate) ([]*ApplyResult, error)

	// GetConstraints 获取行动前必须遵守的约束
	GetConstraints(ctx context.Context, scope string, targetIDs []string) ([]MemoryConstraint, error)

	// GetRelevantMemories 检索相关记忆
	GetRelevantMemories(ctx context.Context, req *RetrievalRequest) ([]*MemoryWithSource, error)

	// ForgetMemory 明确遗忘记忆
	ForgetMemory(ctx context.Context, memoryID string, reason string, operatorID string) error

	// GetMemoryWithEvidence 获取记忆及其证据
	GetMemoryWithEvidence(ctx context.Context, memoryID string) (*MemoryWithSource, error)
}

// RetrievalRequest 表示检索请求
type RetrievalRequest struct {
	Scope          string      `json:"scope"`
	Query          string      `json:"query"`
	TargetIDs      []string    `json:"target_ids,omitempty"` // 明确提及的用户
	Limit          int         `json:"limit"`
	IncludeTypes   []MemoryType `json:"include_types,omitempty"`
	ExcludeExpired bool        `json:"exclude_expired"`
}

// ApplyResult 表示应用候选的结果
type ApplyResult struct {
	Candidate    *MemoryCandidate `json:"candidate"`
	Success      bool             `json:"success"`
	MemoryID     string           `json:"memory_id,omitempty"`
	Status       MemoryStatus     `json:"status,omitempty"`
	Error        string           `json:"error,omitempty"`
	ConflictWith string           `json:"conflict_with,omitempty"` // 冲突的记忆ID
}

// ValidateCandidate 校验候选的基本结构
func ValidateCandidate(c *MemoryCandidate) error {
	if c.Scope == "" {
		return fmt.Errorf("scope is required")
	}
	if c.SubjectKind == "" {
		return fmt.Errorf("subject_kind is required")
	}
	if c.SubjectID == "" {
		return fmt.Errorf("subject_id is required")
	}
	if c.Type == "" {
		return fmt.Errorf("type is required")
	}
	if c.Content == "" && c.Predicate == "" {
		return fmt.Errorf("content or predicate is required")
	}
	if len(c.EvidenceEventIDs) == 0 {
		return fmt.Errorf("at least one evidence event is required")
	}

	// 情景记忆必须有参与者和锚点
	if c.Type == MemoryTypeEpisodic {
		if len(c.ParticipantIDs) == 0 {
			return fmt.Errorf("episodic memory requires participants")
		}
		if c.AnchorEventID == "" {
			return fmt.Errorf("episodic memory requires anchor_event_id")
		}
	}

	// 结构化事实必须有 predicate 和 normalized_value
	if c.Predicate != "" {
		if c.NormalizedValue == "" {
			return fmt.Errorf("predicate requires normalized_value")
		}
		if c.Qualifier == "" {
			c.Qualifier = "default"
		}
	}

	return nil
}

// IsTemporary 判断约束是否为临时的
func (c *MemoryConstraint) IsTemporary() bool {
	return c.ValidUntil != nil && c.ValidUntil.After(time.Now())
}

// IsValid 判断约束是否当前有效
func (c *MemoryConstraint) IsValid() bool {
	if c.ValidUntil == nil {
		return true
	}
	return time.Now().Before(*c.ValidUntil)
}

// IsActive 判断记忆是否当前有效
func (m *Memory) IsActive() bool {
	if m.Status != MemoryStatusActive {
		return false
	}
	if m.ValidUntil != nil && time.Now().After(*m.ValidUntil) {
		return false
	}
	return true
}

// GetFactKey 获取事实键标识
func (m *Memory) GetFactKey() string {
	if m.Predicate == "" {
		return ""
	}
	return fmt.Sprintf("%s:%s:%s:%s:%s",
		m.Scope, m.SubjectKind, m.SubjectID, m.Predicate, m.Qualifier)
}
