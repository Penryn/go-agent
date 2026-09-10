package memory_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/phlin/go-agent/internal/application/memory"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

// mockStore 是用于测试的 mock store
type mockStore struct {
	memories  map[string]*memorydomain.Memory
	evidence  map[string][]memorydomain.Evidence
	changes   map[string][]*memorydomain.Change
	progress  map[string]*memorydomain.LearningEventProgress
}

func newMockStore() *mockStore {
	return &mockStore{
		memories: make(map[string]*memorydomain.Memory),
		evidence: make(map[string][]memorydomain.Evidence),
		changes:  make(map[string][]*memorydomain.Change),
		progress: make(map[string]*memorydomain.LearningEventProgress),
	}
}

func (m *mockStore) Save(ctx context.Context, mem *memorydomain.Memory, evidence []memorydomain.Evidence, change *memorydomain.Change) error {
	m.memories[mem.MemoryID] = mem
	if len(evidence) > 0 {
		m.evidence[mem.MemoryID] = append(m.evidence[mem.MemoryID], evidence...)
	}
	if change != nil {
		m.changes[mem.MemoryID] = append(m.changes[mem.MemoryID], change)
	}
	return nil
}

func (m *mockStore) Get(ctx context.Context, memoryID string) (*memorydomain.Memory, error) {
	mem, ok := m.memories[memoryID]
	if !ok {
		return nil, fmt.Errorf("memory not found: %s", memoryID)
	}
	return mem, nil
}

func (m *mockStore) ListBySubject(ctx context.Context, scope string, subjectKind memorydomain.SubjectKind, subjectID string, statuses []memorydomain.MemoryStatus) ([]*memorydomain.Memory, error) {
	result := []*memorydomain.Memory{}
	for _, mem := range m.memories {
		if mem.Scope == scope && mem.SubjectKind == subjectKind && mem.SubjectID == subjectID {
			for _, status := range statuses {
				if mem.Status == status {
					result = append(result, mem)
					break
				}
			}
		}
	}
	return result, nil
}

func (m *mockStore) ListByFactKey(ctx context.Context, scope string, subjectKind memorydomain.SubjectKind, subjectID string, predicate memorydomain.Predicate, qualifier string) ([]*memorydomain.Memory, error) {
	result := []*memorydomain.Memory{}
	for _, mem := range m.memories {
		if mem.Scope == scope && mem.SubjectKind == subjectKind && mem.SubjectID == subjectID && mem.Predicate == predicate {
			if qualifier == "" || mem.Qualifier == qualifier {
				result = append(result, mem)
			}
		}
	}
	return result, nil
}

func (m *mockStore) ListByParticipant(ctx context.Context, scope string, participantID string, statuses []memorydomain.MemoryStatus) ([]*memorydomain.Memory, error) {
	result := []*memorydomain.Memory{}
	for _, mem := range m.memories {
		if mem.Scope == scope {
			for _, pid := range mem.ParticipantIDs {
				if pid == participantID {
					for _, status := range statuses {
						if mem.Status == status {
							result = append(result, mem)
							break
						}
					}
					break
				}
			}
		}
	}
	return result, nil
}

func (m *mockStore) GetEvidence(ctx context.Context, memoryID string) ([]memorydomain.Evidence, error) {
	return m.evidence[memoryID], nil
}

func (m *mockStore) GetChanges(ctx context.Context, memoryID string, limit int) ([]*memorydomain.Change, error) {
	changes := m.changes[memoryID]
	if len(changes) > limit {
		return changes[:limit], nil
	}
	return changes, nil
}

func (m *mockStore) LockFactKey(ctx context.Context, scope string, subjectKind memorydomain.SubjectKind, subjectID string, predicate memorydomain.Predicate, qualifier string) error {
	return nil // Mock 不需要锁
}

func (m *mockStore) MarkProgress(ctx context.Context, progress *memorydomain.LearningEventProgress) error {
	key := progress.EventID + ":" + progress.ExtractorVersion
	m.progress[key] = progress
	return nil
}

func (m *mockStore) GetProgress(ctx context.Context, eventID string, extractorVersion string) (*memorydomain.LearningEventProgress, error) {
	key := eventID + ":" + extractorVersion
	return m.progress[key], nil
}

func (m *mockStore) ListUnprocessedEvents(ctx context.Context, groupID int64, extractorVersion string, limit int) ([]string, error) {
	return []string{}, nil
}

// TestApplyCandidates_New 测试创建新记忆
func TestApplyCandidates_New(t *testing.T) {
	store := newMockStore()
	service := memory.NewService(store)

	candidates := []*memorydomain.MemoryCandidate{
		{
			Scope:            "group_123",
			SubjectKind:      memorydomain.SubjectKindUser,
			SubjectID:        "user_456",
			Type:             memorydomain.MemoryTypeSemantic,
			Subtype:          "preference",
			Content:          "张三喜欢乌龙茶",
			EvidenceEventIDs: []string{"event_001"},
			SourceRole:       "primary",
			Intent:           "new",
			ExtractorVersion: "v1",
			ObservedAt:       time.Now(),
		},
	}

	results, err := service.ApplyCandidates(context.Background(), candidates)

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Success)
	assert.NotEmpty(t, results[0].MemoryID)
	assert.Equal(t, memorydomain.MemoryStatusActive, results[0].Status)

	// 验证记忆被保存
	assert.Len(t, store.memories, 1)
	assert.Len(t, store.evidence[results[0].MemoryID], 1)
	assert.Len(t, store.changes[results[0].MemoryID], 1)
}

// TestApplyCandidates_Correct 测试更正记忆
func TestApplyCandidates_Correct(t *testing.T) {
	store := newMockStore()
	service := memory.NewService(store)

	// 1. 创建初始记忆
	oldMemoryID := uuid.New().String()
	store.memories[oldMemoryID] = &memorydomain.Memory{
		MemoryID:        oldMemoryID,
		Scope:           "group_123",
		SubjectKind:     memorydomain.SubjectKindUser,
		SubjectID:       "user_456",
		Type:            memorydomain.MemoryTypeSemantic,
		Content:         "张三喜欢乌龙茶",
		Status:          memorydomain.MemoryStatusActive,
		Revision:        1,
		FirstObservedAt: time.Now().Add(-24 * time.Hour),
		LastObservedAt:  time.Now().Add(-24 * time.Hour),
		CreatedAt:       time.Now().Add(-24 * time.Hour),
		UpdatedAt:       time.Now().Add(-24 * time.Hour),
	}

	// 2. 更正记忆
	candidates := []*memorydomain.MemoryCandidate{
		{
			Scope:            "group_123",
			SubjectKind:      memorydomain.SubjectKindUser,
			SubjectID:        "user_456",
			Type:             memorydomain.MemoryTypeSemantic,
			Content:          "张三喜欢红茶",
			EvidenceEventIDs: []string{"event_002"},
			SourceRole:       "primary",
			Intent:           "correct",
			SupersedesID:     oldMemoryID,
			ExtractorVersion: "v1",
			ObservedAt:       time.Now(),
		},
	}

	results, err := service.ApplyCandidates(context.Background(), candidates)

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Success)

	// 验证旧记忆被标记为 superseded
	oldMem := store.memories[oldMemoryID]
	assert.Equal(t, memorydomain.MemoryStatusSuperseded, oldMem.Status)
	assert.Equal(t, int64(2), oldMem.Revision)

	// 验证新记忆创建
	newMem := store.memories[results[0].MemoryID]
	assert.Equal(t, memorydomain.MemoryStatusActive, newMem.Status)
	assert.Equal(t, "张三喜欢红茶", newMem.Content)
	assert.Equal(t, oldMemoryID, newMem.SupersedesID)
}

// TestGetConstraints 测试获取约束
func TestGetConstraints(t *testing.T) {
	store := newMockStore()
	service := memory.NewService(store)

	// 创建测试约束
	now := time.Now()
	validUntil := now.Add(24 * time.Hour)

	store.memories["mem_1"] = &memorydomain.Memory{
		MemoryID:        "mem_1",
		Scope:           "group_123",
		SubjectKind:     memorydomain.SubjectKindUser,
		SubjectID:       "user_456",
		Type:            memorydomain.MemoryTypeSocial,
		Predicate:       memorydomain.PredicatePreferredName,
		NormalizedValue: "老张",
		Qualifier:       "default",
		Status:          memorydomain.MemoryStatusActive,
		Revision:        1,
		FirstObservedAt: now,
		LastObservedAt:  now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	store.memories["mem_2"] = &memorydomain.Memory{
		MemoryID:        "mem_2",
		Scope:           "group_123",
		SubjectKind:     memorydomain.SubjectKindUser,
		SubjectID:       "user_456",
		Type:            memorydomain.MemoryTypeSocial,
		Predicate:       memorydomain.PredicateAllowMention,
		NormalizedValue: "false",
		Qualifier:       "default",
		Status:          memorydomain.MemoryStatusActive,
		ValidUntil:      &validUntil,
		Revision:        1,
		FirstObservedAt: now,
		LastObservedAt:  now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	// 获取约束
	constraints, err := service.GetConstraints(context.Background(), "group_123", []string{"user_456"})

	require.NoError(t, err)
	require.Len(t, constraints, 2)

	// 验证约束内容
	var preferredName, allowMention *memorydomain.MemoryConstraint
	for i := range constraints {
		if constraints[i].Type == "preferred_name" {
			preferredName = &constraints[i]
		} else if constraints[i].Type == "allow_mention" {
			allowMention = &constraints[i]
		}
	}

	require.NotNil(t, preferredName)
	assert.Equal(t, "老张", preferredName.Value)

	require.NotNil(t, allowMention)
	assert.Equal(t, "false", allowMention.Value)
	assert.True(t, allowMention.IsTemporary())
	assert.True(t, allowMention.IsValid())
}

// TestForgetMemory 测试遗忘
func TestForgetMemory(t *testing.T) {
	store := newMockStore()
	service := memory.NewService(store)

	// 创建测试记忆
	memoryID := uuid.New().String()
	store.memories[memoryID] = &memorydomain.Memory{
		MemoryID:        memoryID,
		Scope:           "group_123",
		SubjectKind:     memorydomain.SubjectKindUser,
		SubjectID:       "user_456",
		Type:            memorydomain.MemoryTypeSemantic,
		Content:         "敏感信息",
		Status:          memorydomain.MemoryStatusActive,
		Revision:        1,
		FirstObservedAt: time.Now(),
		LastObservedAt:  time.Now(),
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	// 遗忘
	err := service.ForgetMemory(context.Background(), memoryID, "user requested", "user_456")

	require.NoError(t, err)

	// 验证状态
	mem := store.memories[memoryID]
	assert.Equal(t, memorydomain.MemoryStatusRevoked, mem.Status)
	assert.Equal(t, int64(2), mem.Revision)

	// 验证变更记录
	changes := store.changes[memoryID]
	require.Len(t, changes, 1)
	assert.Equal(t, "revoked", changes[0].ChangeKind)
	assert.Equal(t, "user requested", changes[0].Reason)
}
