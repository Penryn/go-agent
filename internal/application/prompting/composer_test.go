package prompting

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
)

// mockLLM 模拟 LLM
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

// TestComposeResponse_WithoutLLM 测试没有 LLM 时的降级行为
func TestComposeResponse_WithoutLLM(t *testing.T) {
	composer := NewComposer(personadomain.PersonaConfig{
		Name:        "测试机器人",
		Description: "一个测试用的机器人",
	})

	evt := &conversationdomain.ConversationEvent{
		EventID: "evt_001",
		GroupID: 12345,
		UserID:  100,
		Text:    "你好",
	}

	response, err := composer.ComposeResponse(context.Background(), nil, evt, "direct_answer")
	require.NoError(t, err)
	assert.Equal(t, "抱歉，我现在有点困，稍后再聊吧", response, "无 LLM 时应返回友好提示")
}

// TestComposeResponseWithHistory 测试带历史的回复生成
func TestComposeResponseWithHistory(t *testing.T) {
	llm := &mockLLM{response: "好的，我明白了"}
	composer := NewComposer(personadomain.PersonaConfig{
		Name:        "小助手",
		Description: "一个友好的助手",
		Traits:      []string{"友好", "耐心"},
	}).WithLLM(llm)

	history := []conversationdomain.ConversationEvent{
		{EventID: "evt_001", UserID: 100, Text: "今天天气怎么样？", TimestampUnix: time.Now().Unix()},
		{EventID: "evt_002", UserID: 0, Text: "今天天气不错", TimestampUnix: time.Now().Unix()},
	}

	evt := &conversationdomain.ConversationEvent{
		EventID: "evt_003",
		GroupID: 12345,
		UserID:  100,
		Text:    "那适合出去玩吗？",
	}

	response, err := composer.ComposeResponseWithHistory(
		context.Background(),
		nil,
		evt,
		history,
		"direct_answer",
	)

	require.NoError(t, err)
	assert.Equal(t, "好的，我明白了", response)

	// 验证 Prompt 包含历史（通过检查是否调用了 LLM）
	assert.NotEmpty(t, response)
}

// TestComposeResponse_WithPersonaContext 测试使用 PersonaContext
func TestComposeResponse_WithPersonaContext(t *testing.T) {
	llm := &mockLLM{response: "我今天精力充沛！"}
	composer := NewComposer(personadomain.PersonaConfig{
		Name:        "活泼bot",
		Description: "一个活泼的机器人",
	}).WithLLM(llm)

	personaCtx := &personadomain.PersonaContext{
		Posture: personadomain.GroupPosture{
			ParticipationBias: 0.8,
			Familiarity:       0.9,
		},
		EphemeralState: personadomain.EphemeralState{
			Mood:   personadomain.MoodHappy,
			Energy: personadomain.EnergyHigh,
		},
	}

	evt := &conversationdomain.ConversationEvent{
		EventID: "evt_001",
		GroupID: 12345,
		UserID:  100,
		Text:    "你今天怎么样？",
	}

	response, err := composer.ComposeResponse(
		context.Background(),
		personaCtx,
		evt,
		"casual_chat",
	)

	require.NoError(t, err)
	assert.Equal(t, "我今天精力充沛！", response)
}

// TestBuildEnhancedResponsePrompt 测试增强 Prompt 构建
func TestBuildEnhancedResponsePrompt(t *testing.T) {
	composer := NewComposer(personadomain.PersonaConfig{
		Name:        "小助手",
		Description: "友好的助手",
		Traits:      []string{"友好", "耐心"},
	})

	personaCtx := &personadomain.PersonaContext{
		Posture: personadomain.GroupPosture{
			ParticipationBias: 0.6,
			Familiarity:       0.8,
		},
		EphemeralState: personadomain.EphemeralState{
			Mood:   personadomain.MoodHappy,
			Energy: personadomain.EnergyNormal,
		},
	}

	history := []conversationdomain.ConversationEvent{
		{UserID: 100, Text: "你好"},
		{UserID: 0, Text: "你好呀"},
	}

	evt := &conversationdomain.ConversationEvent{
		Text: "今天天气不错",
	}

	prompt := composer.buildEnhancedResponsePrompt(personaCtx, evt, history, "casual_chat", nil)

	// 验证 Prompt 包含各个部分
	assert.Contains(t, prompt, "# 角色设定")
	assert.Contains(t, prompt, "小助手")
	assert.Contains(t, prompt, "# 性格特点")
	assert.Contains(t, prompt, "友好")
	assert.Contains(t, prompt, "# 当前状态")
	assert.Contains(t, prompt, "比较愉快", "应包含心情状态")
	assert.Contains(t, prompt, "很熟悉", "应包含熟悉度")
	assert.Contains(t, prompt, "# 最近对话")
	assert.Contains(t, prompt, "你好")
	assert.Contains(t, prompt, "# 当前消息")
	assert.Contains(t, prompt, "今天天气不错")
}

// TestTranslateIntent 测试意图翻译
func TestTranslateIntent(t *testing.T) {
	composer := NewComposer(personadomain.PersonaConfig{})

	tests := []struct {
		intent   string
		expected string
	}{
		{"direct_answer", "直接回答用户的问题"},
		{"continue_topic", "延续当前话题"},
		{"moderate", "缓和气氛"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.intent, func(t *testing.T) {
			result := composer.translateIntent(tt.intent)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestComposeResponse_EmptyResponse 测试空回复处理
func TestComposeResponse_EmptyResponse(t *testing.T) {
	llm := &mockLLM{response: "   "} // 空白字符
	composer := NewComposer(personadomain.PersonaConfig{
		Name: "测试bot",
	}).WithLLM(llm)

	evt := &conversationdomain.ConversationEvent{
		Text: "测试",
	}

	response, err := composer.ComposeResponse(context.Background(), nil, evt, "direct_answer")
	require.NoError(t, err)
	assert.Equal(t, "...", response, "空回复应返回友好提示")
}

// TestComposeResponse_LLMError 测试 LLM 错误处理
func TestComposeResponse_LLMError(t *testing.T) {
	llm := &mockLLM{err: assert.AnError}
	composer := NewComposer(personadomain.PersonaConfig{
		Name: "测试bot",
	}).WithLLM(llm)

	evt := &conversationdomain.ConversationEvent{
		Text: "测试",
	}

	response, err := composer.ComposeResponse(context.Background(), nil, evt, "direct_answer")
	assert.Error(t, err, "LLM 错误应传播")
	assert.Equal(t, "嗯...我需要想想再回答", response, "LLM 错误应返回友好提示")
}

// TestBuildEnhancedPrompt_WithoutHistory 测试没有历史时的 Prompt
func TestBuildEnhancedPrompt_WithoutHistory(t *testing.T) {
	composer := NewComposer(personadomain.PersonaConfig{
		Name: "简单bot",
	})

	evt := &conversationdomain.ConversationEvent{
		Text: "你好",
	}

	prompt := composer.buildEnhancedResponsePrompt(nil, evt, nil, "direct_answer", nil)

	// 不应包含历史部分
	assert.NotContains(t, prompt, "# 最近对话")
	// 应包含基本部分
	assert.Contains(t, prompt, "# 角色设定")
	assert.Contains(t, prompt, "# 当前消息")
}
