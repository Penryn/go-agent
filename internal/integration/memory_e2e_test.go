package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/phlin/go-agent/internal/application/learning"
	"github.com/phlin/go-agent/internal/application/memory"
	"github.com/phlin/go-agent/internal/application/prompting"
	"github.com/phlin/go-agent/internal/domain/conversation"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

// mockMemoryStore 模拟内存存储
type mockMemoryStore struct {
	memories map[string]*memorydomain.Memory
	evidence map[string][]memorydomain.Evidence
	changes  map[string][]memorydomain.Change
	progress map[string]*memorydomain.LearningEventProgress
}

func newMockMemoryStore() *mockMemoryStore {
	return &mockMemoryStore{
		memories: make(map[string]*memorydomain.Memory),
		evidence: make(map[string][]memorydomain.Evidence),
		changes:  make(map[string][]memorydomain.Change),
		progress: make(map[string]*memorydomain.LearningEventProgress),
	}
}

func (m *mockMemoryStore) Save(ctx context.Context, mem *memorydomain.Memory, evidence []memorydomain.Evidence, change *memorydomain.Change) error {
	m.memories[mem.MemoryID] = mem
	m.evidence[mem.MemoryID] = evidence
	if change != nil {
		m.changes[mem.MemoryID] = append(m.changes[mem.MemoryID], *change)
	}
	return nil
}

func (m *mockMemoryStore) Get(ctx context.Context, memoryID string) (*memorydomain.Memory, error) {
	if mem, ok := m.memories[memoryID]; ok {
		return mem, nil
	}
	return nil, nil
}

func (m *mockMemoryStore) ListBySubject(ctx context.Context, scope string, subjectKind memorydomain.SubjectKind, subjectID string, statuses []memorydomain.MemoryStatus) ([]*memorydomain.Memory, error) {
	var result []*memorydomain.Memory
	for _, mem := range m.memories {
		if mem.Scope == scope && mem.SubjectKind == subjectKind && mem.SubjectID == subjectID {
			result = append(result, mem)
		}
	}
	return result, nil
}

func (m *mockMemoryStore) ListByFactKey(ctx context.Context, scope string, subjectKind memorydomain.SubjectKind, subjectID string, predicate memorydomain.Predicate, qualifier string) ([]*memorydomain.Memory, error) {
	var result []*memorydomain.Memory
	for _, mem := range m.memories {
		if mem.Scope == scope && mem.SubjectKind == subjectKind && mem.SubjectID == subjectID && mem.Predicate == predicate {
			if qualifier == "" || mem.Qualifier == qualifier {
				result = append(result, mem)
			}
		}
	}
	return result, nil
}

func (m *mockMemoryStore) ListByParticipant(ctx context.Context, scope string, participantID string, statuses []memorydomain.MemoryStatus) ([]*memorydomain.Memory, error) {
	return nil, nil
}

func (m *mockMemoryStore) GetEvidence(ctx context.Context, memoryID string) ([]memorydomain.Evidence, error) {
	return m.evidence[memoryID], nil
}

func (m *mockMemoryStore) GetChangeHistory(ctx context.Context, memoryID string, limit int) ([]*memorydomain.Change, error) {
	changes := m.changes[memoryID]
	result := make([]*memorydomain.Change, len(changes))
	for i := range changes {
		result[i] = &changes[i]
	}
	return result, nil
}

// GetChanges is an alias for GetChangeHistory
func (m *mockMemoryStore) GetChanges(ctx context.Context, memoryID string, limit int) ([]*memorydomain.Change, error) {
	return m.GetChangeHistory(ctx, memoryID, limit)
}

func (m *mockMemoryStore) LockFactKey(ctx context.Context, scope string, subjectKind memorydomain.SubjectKind, subjectID string, predicate memorydomain.Predicate, qualifier string) error {
	return nil
}

func (m *mockMemoryStore) MarkProgress(ctx context.Context, progress *memorydomain.LearningEventProgress) error {
	key := progress.EventID + "-" + progress.ExtractorVersion
	m.progress[key] = progress
	return nil
}

func (m *mockMemoryStore) GetProgress(ctx context.Context, eventID string, extractorVersion string) (*memorydomain.LearningEventProgress, error) {
	key := eventID + "-" + extractorVersion
	return m.progress[key], nil
}

func (m *mockMemoryStore) ListUnprocessedEvents(ctx context.Context, groupID int64, extractorVersion string, limit int) ([]string, error) {
	return []string{}, nil
}

// mockLLM 模拟 LLM
type mockLLM struct {
	response string
}

func (m *mockLLM) Generate(ctx context.Context, prompt string) (string, error) {
	if m.response != "" {
		return m.response, nil
	}
	// 默认返回一个偏好记忆
	return `[{
		"subject_kind": "user",
		"subject_id": "789",
		"type": "semantic",
		"subtype": "preference",
		"content": "张三喜欢喝乌龙茶",
		"predicate": "likes_topic",
		"normalized_value": "乌龙茶",
		"qualifier": "default",
		"participant_ids": ["789"],
		"bot_role": "observer",
		"anchor_event_id": "0",
		"evidence_event_ids": ["0"],
		"confidence": 0.95,
		"reasoning": "用户明确表达了对乌龙茶的喜好"
	}]`, nil
}

// TestEndToEndMemoryLearning 端到端测试：从对话到记忆到约束
func TestEndToEndMemoryLearning(t *testing.T) {
	ctx := context.Background()

	// 1. 初始化服务
	store := newMockMemoryStore()
	memService := memory.NewService(store)
	llm := &mockLLM{}

	// Note: learningService would be used in production to process batches
	// For this test, we directly test the extraction logic

	// 2. 模拟对话窗口
	window := &learning.Window{
		GroupID:   123456,
		StartTime: time.Now(),
		EndTime:   time.Now().Add(5 * time.Minute),
		Events: []conversation.ConversationEvent{
			{
				EventID:       "event_001",
				UserID:        789,
				Text:          "我最喜欢喝乌龙茶了",
				TimestampUnix: time.Now().Unix(),
				Sender: conversation.SenderIdentity{
					DisplayName: "张三",
				},
			},
		},
	}

	// 3. 提炼记忆
	promptBuilder := learning.NewExtractionPromptBuilder("v1")
	prompt := promptBuilder.BuildPrompt(window)
	require.Contains(t, prompt, "我最喜欢喝乌龙茶了")

	response, err := llm.Generate(ctx, prompt)
	require.NoError(t, err)

	candidates, err := promptBuilder.ParseExtractionResponse(response, window)
	require.NoError(t, err)
	require.Len(t, candidates, 1)

	// 4. 应用候选
	results, err := memService.ApplyCandidates(ctx, candidates)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.True(t, results[0].Success)

	memoryID := results[0].MemoryID
	require.NotEmpty(t, memoryID)

	// 5. 验证记忆已保存
	savedMemory, err := store.Get(ctx, memoryID)
	require.NoError(t, err)
	require.NotNil(t, savedMemory)
	assert.Equal(t, "张三喜欢喝乌龙茶", savedMemory.Content)
	assert.Equal(t, "likes_topic", string(savedMemory.Predicate))
	assert.Equal(t, "乌龙茶", savedMemory.NormalizedValue)

	// 6. 验证证据已保存
	evidence, err := store.GetEvidence(ctx, memoryID)
	require.NoError(t, err)
	require.NotEmpty(t, evidence)
	assert.Equal(t, "event_001", evidence[0].EventID)

	t.Log("✅ 端到端测试通过：对话 -> 提炼 -> 记忆保存")
}

// TestEndToEndConstraintEnforcement 端到端测试：约束提取和执行
func TestEndToEndConstraintEnforcement(t *testing.T) {
	ctx := context.Background()

	// 1. 初始化服务
	store := newMockMemoryStore()
	memService := memory.NewService(store)

	// 2. 创建一个 "不允许 @" 的约束记忆
	llm := &mockLLM{
		response: `[{
			"subject_kind": "user",
			"subject_id": "456",
			"type": "social",
			"subtype": "boundary",
			"content": "李四要求不要 @ 他",
			"predicate": "allow_mention",
			"normalized_value": "false",
			"qualifier": "default",
			"participant_ids": ["456"],
			"bot_role": "observer",
			"anchor_event_id": "event_002",
			"evidence_event_ids": ["event_002"],
			"confidence": 0.98,
			"reasoning": "用户明确表达了互动边界"
		}]`,
	}

	window := &learning.Window{
		GroupID: 123456,
		Events: []conversation.ConversationEvent{
			{EventID: "event_002", TimestampUnix: time.Now().Unix()},
		},
	}

	promptBuilder := learning.NewExtractionPromptBuilder("v1")
	response, _ := llm.Generate(ctx, "")
	candidates, _ := promptBuilder.ParseExtractionResponse(response, window)

	// 应用约束候选
	results, err := memService.ApplyCandidates(ctx, candidates)
	require.NoError(t, err)
	require.True(t, results[0].Success)

	// 3. 获取约束
	constraints, err := memService.GetConstraints(ctx, "group:123456", []string{"456"})
	require.NoError(t, err)
	require.Len(t, constraints, 1)
	assert.Equal(t, "allow_mention", constraints[0].Type)
	assert.Equal(t, "false", constraints[0].Value)

	// 4. 集成到 Composer
	integration := prompting.NewMemoryConstraintIntegration(memService)
	section, err := integration.BuildConstraintSection(ctx, 123456, []int64{456})
	require.NoError(t, err)
	assert.Contains(t, section, "记忆约束")
	assert.Contains(t, section, "禁止 @")
	assert.Contains(t, section, "User456")

	// 5. 校验工具调用
	validator := prompting.NewActionValidator(integration)
	err = validator.ValidateToolCall(ctx, 123456, "mention", map[string]interface{}{
		"target_user_id": int64(456),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "不允许 @")

	t.Log("✅ 端到端测试通过：约束提取 -> Composer 注入 -> 发送前校验")
}

// TestEndToEndMemoryCorrection 端到端测试：记忆更正
func TestEndToEndMemoryCorrection(t *testing.T) {
	ctx := context.Background()

	// 1. 初始化
	store := newMockMemoryStore()
	memService := memory.NewService(store)

	// 2. 创建初始记忆
	initialCandidate := &memorydomain.MemoryCandidate{
		Scope:            "group:123",
		SubjectKind:      memorydomain.SubjectKindUser,
		SubjectID:        "789",
		Type:             memorydomain.MemoryTypeSemantic,
		Subtype:          "preference",
		Content:          "张三喜欢喝绿茶",
		Predicate:        memorydomain.PredicateLikesTopic,
		NormalizedValue:  "绿茶",
		Qualifier:        "default",
		EvidenceEventIDs: []string{"event_001"},
		Intent:           "new",
		ExtractorVersion: "v1",
		ObservedAt:       time.Now(),
	}

	results, _ := memService.ApplyCandidates(ctx, []*memorydomain.MemoryCandidate{initialCandidate})
	require.True(t, results[0].Success)
	oldMemoryID := results[0].MemoryID

	// 3. 更正记忆
	correctionCandidate := &memorydomain.MemoryCandidate{
		Scope:            "group:123",
		SubjectKind:      memorydomain.SubjectKindUser,
		SubjectID:        "789",
		Type:             memorydomain.MemoryTypeSemantic,
		Subtype:          "preference",
		Content:          "张三喜欢喝乌龙茶",
		Predicate:        memorydomain.PredicateLikesTopic,
		NormalizedValue:  "乌龙茶",
		Qualifier:        "default",
		EvidenceEventIDs: []string{"event_002"},
		Intent:           "correct",
		SupersedesID:     oldMemoryID, // 指定要更正的记忆
		ExtractorVersion: "v1",
		ObservedAt:       time.Now(),
	}

	results, err := memService.ApplyCandidates(ctx, []*memorydomain.MemoryCandidate{correctionCandidate})
	require.NoError(t, err)
	require.Len(t, results, 1)
	if !results[0].Success {
		t.Logf("Correction failed: %s", results[0].Error)
	}
	require.True(t, results[0].Success)
	newMemoryID := results[0].MemoryID

	// 4. 验证旧记忆被替换
	oldMemory, _ := store.Get(ctx, oldMemoryID)
	require.NotNil(t, oldMemory)
	assert.Equal(t, memorydomain.MemoryStatusSuperseded, oldMemory.Status)

	// 5. 验证新记忆激活
	newMemory, _ := store.Get(ctx, newMemoryID)
	require.NotNil(t, newMemory)
	assert.Equal(t, memorydomain.MemoryStatusActive, newMemory.Status)
	assert.Equal(t, "张三喜欢喝乌龙茶", newMemory.Content)
	assert.Equal(t, "乌龙茶", newMemory.NormalizedValue)
	assert.Equal(t, oldMemoryID, newMemory.SupersedesID)

	t.Log("✅ 端到端测试通过：记忆更正流程")
}
