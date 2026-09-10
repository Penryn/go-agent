package learning

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/phlin/go-agent/internal/domain/conversation"
)

// mockLLM 模拟 LLM 调用
type mockLLM struct {
	response string
	err      error
}

func (m *mockLLM) Generate(ctx context.Context, prompt string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

// TestExtractionPromptBuilder 测试 Prompt 构建
func TestExtractionPromptBuilder(t *testing.T) {
	builder := NewExtractionPromptBuilder("v1")

	window := &Window{
		GroupID:   123456,
		StartTime: time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 9, 10, 10, 5, 0, 0, time.UTC),
		Events: []conversation.ConversationEvent{
			{
				EventID:       "event_001",
				UserID:        789,
				Text:          "我最喜欢喝乌龙茶了",
				TimestampUnix: time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC).Unix(),
				Sender: conversation.SenderIdentity{
					DisplayName: "张三",
				},
			},
			{
				EventID:       "event_002",
				UserID:        456,
				Text:          "以后别 @ 我了",
				TimestampUnix: time.Date(2026, 9, 10, 10, 1, 0, 0, time.UTC).Unix(),
				Sender: conversation.SenderIdentity{
					DisplayName: "李四",
				},
			},
		},
	}

	prompt := builder.BuildPrompt(window)

	// 验证 Prompt 包含关键信息
	assert.Contains(t, prompt, "记忆提炼任务")
	assert.Contains(t, prompt, "群ID: 123456")
	assert.Contains(t, prompt, "张三")
	assert.Contains(t, prompt, "我最喜欢喝乌龙茶了")
	assert.Contains(t, prompt, "李四")
	assert.Contains(t, prompt, "以后别 @ 我了")
	assert.Contains(t, prompt, "subject_kind")
	assert.Contains(t, prompt, "predicate")
	assert.Contains(t, prompt, "confidence")
}

// TestParseExtractionResponse 测试解析 LLM 响应
func TestParseExtractionResponse(t *testing.T) {
	builder := NewExtractionPromptBuilder("v1")

	window := &Window{
		GroupID: 123456,
		Events: []conversation.ConversationEvent{
			{EventID: "event_001", TimestampUnix: time.Now().Unix()},
			{EventID: "event_002", TimestampUnix: time.Now().Unix()},
		},
	}

	t.Run("valid response", func(t *testing.T) {
		response := `[
  {
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
  }
]`

		candidates, err := builder.ParseExtractionResponse(response, window)

		require.NoError(t, err)
		require.Len(t, candidates, 1)

		c := candidates[0]
		assert.Equal(t, "group_123456", c.Scope)
		assert.Equal(t, "user", string(c.SubjectKind))
		assert.Equal(t, "789", c.SubjectID)
		assert.Equal(t, "semantic", string(c.Type))
		assert.Equal(t, "preference", c.Subtype)
		assert.Equal(t, "张三喜欢喝乌龙茶", c.Content)
		assert.Equal(t, "likes_topic", string(c.Predicate))
		assert.Equal(t, "乌龙茶", c.NormalizedValue)
		assert.Equal(t, "event_001", c.AnchorEventID)
		assert.Equal(t, []string{"event_001"}, c.EvidenceEventIDs)
		assert.Equal(t, "v1", c.ExtractorVersion)
	})

	t.Run("empty response", func(t *testing.T) {
		candidates, err := builder.ParseExtractionResponse("[]", window)

		require.NoError(t, err)
		assert.Empty(t, candidates)
	})

	t.Run("response with markdown code block", func(t *testing.T) {
		response := "```json\n[]\n```"

		candidates, err := builder.ParseExtractionResponse(response, window)

		require.NoError(t, err)
		assert.Empty(t, candidates)
	})

	t.Run("low confidence filtered out", func(t *testing.T) {
		response := `[
  {
    "subject_kind": "user",
    "subject_id": "789",
    "type": "semantic",
    "subtype": "preference",
    "content": "可能喜欢某物",
    "predicate": "",
    "normalized_value": "",
    "qualifier": "default",
    "participant_ids": ["789"],
    "bot_role": "observer",
    "anchor_event_id": "0",
    "evidence_event_ids": ["0"],
    "confidence": 0.5,
    "reasoning": "不确定"
  }
]`

		candidates, err := builder.ParseExtractionResponse(response, window)

		require.NoError(t, err)
		assert.Empty(t, candidates) // 0.5 < 0.7，应该被过滤
	})
}

// TestExtractFromWindow 测试完整的提炼流程
func TestExtractFromWindow(t *testing.T) {
	// 准备 mock LLM
	llmResponse := `[
  {
    "subject_kind": "user",
    "subject_id": "789",
    "type": "social",
    "subtype": "boundary",
    "content": "李四要求不要 @ 他",
    "predicate": "allow_mention",
    "normalized_value": "false",
    "qualifier": "default",
    "participant_ids": ["789"],
    "bot_role": "observer",
    "anchor_event_id": "1",
    "evidence_event_ids": ["1"],
    "confidence": 0.98,
    "reasoning": "用户明确表达了互动边界"
  }
]`

	mockLLM := &mockLLM{response: llmResponse}

	// 创建学习服务
	service := &NewService{
		llm:              mockLLM,
		promptBuilder:    NewExtractionPromptBuilder("v1"),
		extractorVersion: "v1",
	}

	window := &Window{
		GroupID: 123456,
		Events: []conversation.ConversationEvent{
			{EventID: "event_001", TimestampUnix: time.Now().Unix()},
			{EventID: "event_002", TimestampUnix: time.Now().Unix()},
		},
	}

	candidates, err := service.extractFromWindow(context.Background(), window)

	require.NoError(t, err)
	require.Len(t, candidates, 1)

	c := candidates[0]
	assert.Equal(t, "social", string(c.Type))
	assert.Equal(t, "boundary", c.Subtype)
	assert.Equal(t, "allow_mention", string(c.Predicate))
	assert.Equal(t, "false", c.NormalizedValue)
}

// TestMapEventIDs 测试事件 ID 映射
func TestMapEventIDs(t *testing.T) {
	builder := NewExtractionPromptBuilder("v1")

	window := &Window{
		Events: []conversation.ConversationEvent{
			{EventID: "event_aaa"},
			{EventID: "event_bbb"},
			{EventID: "event_ccc"},
		},
	}

	t.Run("map from index", func(t *testing.T) {
		result := builder.mapEventID("0", window)
		assert.Equal(t, "event_aaa", result)

		result = builder.mapEventID("1", window)
		assert.Equal(t, "event_bbb", result)

		result = builder.mapEventID("2", window)
		assert.Equal(t, "event_ccc", result)
	})

	t.Run("out of range", func(t *testing.T) {
		result := builder.mapEventID("999", window)
		assert.Empty(t, result)

		result = builder.mapEventID("-1", window)
		assert.Empty(t, result)
	})

	t.Run("already event ID", func(t *testing.T) {
		result := builder.mapEventID("event_xyz", window)
		assert.Equal(t, "event_xyz", result) // 直接返回
	})

	t.Run("batch mapping", func(t *testing.T) {
		result := builder.mapEventIDs([]string{"0", "2"}, window)
		assert.Equal(t, []string{"event_aaa", "event_ccc"}, result)
	})
}
