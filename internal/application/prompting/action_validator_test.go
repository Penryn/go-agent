package prompting

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

// TestValidateBeforeSend 测试发送前校验
func TestValidateBeforeSend(t *testing.T) {
	t.Run("no constraints - allow all", func(t *testing.T) {
		mockService := &mockMemoryConstraintService{
			constraints: []memorydomain.MemoryConstraint{},
		}

		integration := NewMemoryConstraintIntegration(mockService)
		validator := NewActionValidator(integration)

		err := validator.ValidateBeforeSend(context.Background(), 123, ActionIntent{
			Type:         "mention",
			TargetUserID: 456,
		})

		assert.NoError(t, err)
	})

	t.Run("mention blocked", func(t *testing.T) {
		mockService := &mockMemoryConstraintService{
			constraints: []memorydomain.MemoryConstraint{
				{
					SubjectID: "456",
					Type:      "allow_mention",
					Value:     "false",
				},
			},
		}

		integration := NewMemoryConstraintIntegration(mockService)
		validator := NewActionValidator(integration)

		err := validator.ValidateBeforeSend(context.Background(), 123, ActionIntent{
			Type:         "mention",
			TargetUserID: 456,
		})

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "不允许 @")
	})

	t.Run("poke blocked", func(t *testing.T) {
		mockService := &mockMemoryConstraintService{
			constraints: []memorydomain.MemoryConstraint{
				{
					SubjectID: "456",
					Type:      "allow_poke",
					Value:     "false",
				},
			},
		}

		integration := NewMemoryConstraintIntegration(mockService)
		validator := NewActionValidator(integration)

		err := validator.ValidateBeforeSend(context.Background(), 123, ActionIntent{
			Type:         "poke",
			TargetUserID: 456,
		})

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "不允许戳一戳")
	})

	t.Run("expired constraint - allow", func(t *testing.T) {
		expired := time.Now().Add(-1 * time.Hour)

		mockService := &mockMemoryConstraintService{
			constraints: []memorydomain.MemoryConstraint{
				{
					SubjectID:  "456",
					Type:       "allow_mention",
					Value:      "false",
					ValidUntil: &expired,
				},
			},
		}

		integration := NewMemoryConstraintIntegration(mockService)
		validator := NewActionValidator(integration)

		err := validator.ValidateBeforeSend(context.Background(), 123, ActionIntent{
			Type:         "mention",
			TargetUserID: 456,
		})

		assert.NoError(t, err)
	})
}

// TestExtractActionFromToolCall 测试从工具调用提取动作意图
func TestExtractActionFromToolCall(t *testing.T) {
	t.Run("extract mention", func(t *testing.T) {
		intent := ExtractActionFromToolCall("mention", map[string]interface{}{
			"target_user_id": int64(456),
			"content":        "你好",
		})

		assert.Equal(t, "mention", intent.Type)
		assert.Equal(t, int64(456), intent.TargetUserID)
		assert.Equal(t, "你好", intent.Content)
	})

	t.Run("extract poke with user_id field", func(t *testing.T) {
		intent := ExtractActionFromToolCall("poke", map[string]interface{}{
			"user_id": int64(789),
		})

		assert.Equal(t, "poke", intent.Type)
		assert.Equal(t, int64(789), intent.TargetUserID)
	})

	t.Run("extract speak_text with text field", func(t *testing.T) {
		intent := ExtractActionFromToolCall("speak_text", map[string]interface{}{
			"text": "大家好",
		})

		assert.Equal(t, "speak_text", intent.Type)
		assert.Equal(t, "大家好", intent.Content)
	})

	t.Run("missing fields", func(t *testing.T) {
		intent := ExtractActionFromToolCall("some_action", map[string]interface{}{})

		assert.Equal(t, "some_action", intent.Type)
		assert.Equal(t, int64(0), intent.TargetUserID)
		assert.Empty(t, intent.Content)
	})
}

// TestValidateToolCall 测试工具调用校验
func TestValidateToolCall(t *testing.T) {
	t.Run("mention tool blocked", func(t *testing.T) {
		mockService := &mockMemoryConstraintService{
			constraints: []memorydomain.MemoryConstraint{
				{
					SubjectID: "456",
					Type:      "allow_mention",
					Value:     "false",
				},
			},
		}

		integration := NewMemoryConstraintIntegration(mockService)
		validator := NewActionValidator(integration)

		err := validator.ValidateToolCall(context.Background(), 123, "mention", map[string]interface{}{
			"target_user_id": int64(456),
			"content":        "你好",
		})

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "不允许 @")
	})

	t.Run("poke tool blocked", func(t *testing.T) {
		mockService := &mockMemoryConstraintService{
			constraints: []memorydomain.MemoryConstraint{
				{
					SubjectID: "456",
					Type:      "allow_poke",
					Value:     "false",
				},
			},
		}

		integration := NewMemoryConstraintIntegration(mockService)
		validator := NewActionValidator(integration)

		err := validator.ValidateToolCall(context.Background(), 123, "poke", map[string]interface{}{
			"user_id": int64(456),
		})

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "不允许戳一戳")
	})

	t.Run("speak_text tool - no constraint", func(t *testing.T) {
		mockService := &mockMemoryConstraintService{
			constraints: []memorydomain.MemoryConstraint{},
		}

		integration := NewMemoryConstraintIntegration(mockService)
		validator := NewActionValidator(integration)

		err := validator.ValidateToolCall(context.Background(), 123, "speak_text", map[string]interface{}{
			"text": "大家好",
		})

		assert.NoError(t, err)
	})
}
