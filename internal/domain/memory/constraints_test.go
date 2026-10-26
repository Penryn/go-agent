package memory_test

import (
	"testing"
	"time"

	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	"github.com/stretchr/testify/assert"
)

type constraintFixture struct {
	constraints []memorydomain.MemoryConstraint
}

// TestValidateConstraints 测试约束校验
func TestValidateConstraints(t *testing.T) {
	t.Run("no violation", func(t *testing.T) {
		mockService := &constraintFixture{
			constraints: []memorydomain.MemoryConstraint{
				{
					SubjectID: "456",
					Type:      "preferred_name",
					Value:     "老张",
				},
			},
		}

		err := memorydomain.ValidateInteraction(mockService.constraints, "mention", "456")

		assert.NoError(t, err)
	})

	t.Run("mention violation", func(t *testing.T) {
		mockService := &constraintFixture{
			constraints: []memorydomain.MemoryConstraint{
				{
					SubjectID: "456",
					Type:      "allow_mention",
					Value:     "false",
				},
			},
		}

		err := memorydomain.ValidateInteraction(mockService.constraints, "mention", "456")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "不允许 @")
		assert.Contains(t, err.Error(), "User456")
	})

	t.Run("poke violation", func(t *testing.T) {
		mockService := &constraintFixture{
			constraints: []memorydomain.MemoryConstraint{
				{
					SubjectID: "456",
					Type:      "allow_poke",
					Value:     "false",
				},
			},
		}

		err := memorydomain.ValidateInteraction(mockService.constraints, "poke", "456")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "不允许戳一戳")
	})

	t.Run("expired constraint ignored", func(t *testing.T) {
		expired := time.Now().Add(-1 * time.Hour)

		mockService := &constraintFixture{
			constraints: []memorydomain.MemoryConstraint{
				{
					SubjectID:  "456",
					Type:       "allow_mention",
					Value:      "false",
					ValidUntil: &expired,
				},
			},
		}

		err := memorydomain.ValidateInteraction(mockService.constraints, "mention", "456")

		assert.NoError(t, err) // 已过期，不应报错
	})

	t.Run("different target user", func(t *testing.T) {
		mockService := &constraintFixture{
			constraints: []memorydomain.MemoryConstraint{
				{
					SubjectID: "456",
					Type:      "allow_mention",
					Value:     "false",
				},
			},
		}

		err := memorydomain.ValidateInteraction(mockService.constraints, "mention", "789")

		assert.NoError(t, err) // 不同用户，不应报错
	})
}
