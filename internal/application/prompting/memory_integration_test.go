package prompting

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

// mockMemoryConstraintService 模拟记忆约束服务
type mockMemoryConstraintService struct {
	constraints []memorydomain.MemoryConstraint
	err         error
}

func (m *mockMemoryConstraintService) GetConstraints(ctx context.Context, scope string, targetIDs []string) ([]memorydomain.MemoryConstraint, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.constraints, nil
}

// TestBuildConstraintSection 测试约束部分构建
func TestBuildConstraintSection(t *testing.T) {
	t.Run("no constraints", func(t *testing.T) {
		mockService := &mockMemoryConstraintService{
			constraints: []memorydomain.MemoryConstraint{},
		}

		integration := NewMemoryConstraintIntegration(mockService)
		section, err := integration.BuildConstraintSection(context.Background(), 123, []int64{456})

		require.NoError(t, err)
		assert.Empty(t, section)
	})

	t.Run("preferred name constraint", func(t *testing.T) {
		mockService := &mockMemoryConstraintService{
			constraints: []memorydomain.MemoryConstraint{
				{
					SubjectID: "456",
					Type:      "preferred_name",
					Value:     "老张",
				},
			},
		}

		integration := NewMemoryConstraintIntegration(mockService)
		section, err := integration.BuildConstraintSection(context.Background(), 123, []int64{456})

		require.NoError(t, err)
		assert.Contains(t, section, "记忆约束")
		assert.Contains(t, section, "称呼方式")
		assert.Contains(t, section, "User456")
		assert.Contains(t, section, "老张")
		assert.Contains(t, section, "必须使用这些称呼")
	})

	t.Run("allow mention false", func(t *testing.T) {
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
		section, err := integration.BuildConstraintSection(context.Background(), 123, []int64{456})

		require.NoError(t, err)
		assert.Contains(t, section, "禁止 @")
		assert.Contains(t, section, "User456")
	})

	t.Run("allow poke false", func(t *testing.T) {
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
		section, err := integration.BuildConstraintSection(context.Background(), 123, []int64{456})

		require.NoError(t, err)
		assert.Contains(t, section, "禁止戳一戳")
		assert.Contains(t, section, "User456")
	})

	t.Run("temporary constraint", func(t *testing.T) {
		validUntil := time.Now().Add(1 * time.Hour)

		mockService := &mockMemoryConstraintService{
			constraints: []memorydomain.MemoryConstraint{
				{
					SubjectID:  "456",
					Type:       "allow_mention",
					Value:      "false",
					ValidUntil: &validUntil,
				},
			},
		}

		integration := NewMemoryConstraintIntegration(mockService)
		section, err := integration.BuildConstraintSection(context.Background(), 123, []int64{456})

		require.NoError(t, err)
		assert.Contains(t, section, "临时")
		assert.Contains(t, section, "User456")
	})

	t.Run("multiple constraints", func(t *testing.T) {
		mockService := &mockMemoryConstraintService{
			constraints: []memorydomain.MemoryConstraint{
				{
					SubjectID: "456",
					Type:      "preferred_name",
					Value:     "老张",
				},
				{
					SubjectID: "789",
					Type:      "preferred_name",
					Value:     "小李",
				},
				{
					SubjectID: "456",
					Type:      "allow_mention",
					Value:     "false",
				},
			},
		}

		integration := NewMemoryConstraintIntegration(mockService)
		section, err := integration.BuildConstraintSection(context.Background(), 123, []int64{456, 789})

		require.NoError(t, err)
		assert.Contains(t, section, "记忆约束")
		assert.Contains(t, section, "老张")
		assert.Contains(t, section, "小李")
		assert.Contains(t, section, "禁止 @")
	})
}

// TestValidateConstraints 测试约束校验
func TestValidateConstraints(t *testing.T) {
	t.Run("no violation", func(t *testing.T) {
		mockService := &mockMemoryConstraintService{
			constraints: []memorydomain.MemoryConstraint{
				{
					SubjectID: "456",
					Type:      "preferred_name",
					Value:     "老张",
				},
			},
		}

		integration := NewMemoryConstraintIntegration(mockService)
		err := integration.ValidateConstraints(context.Background(), 123, []int64{456}, "mention", 456)

		assert.NoError(t, err)
	})

	t.Run("mention violation", func(t *testing.T) {
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
		err := integration.ValidateConstraints(context.Background(), 123, []int64{456}, "mention", 456)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "不允许 @")
		assert.Contains(t, err.Error(), "User456")
	})

	t.Run("poke violation", func(t *testing.T) {
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
		err := integration.ValidateConstraints(context.Background(), 123, []int64{456}, "poke", 456)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "不允许戳一戳")
	})

	t.Run("expired constraint ignored", func(t *testing.T) {
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
		err := integration.ValidateConstraints(context.Background(), 123, []int64{456}, "mention", 456)

		assert.NoError(t, err) // 已过期，不应报错
	})

	t.Run("different target user", func(t *testing.T) {
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
		err := integration.ValidateConstraints(context.Background(), 123, []int64{456}, "mention", 789)

		assert.NoError(t, err) // 不同用户，不应报错
	})
}
