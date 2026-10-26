package prompting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

type constraintFixture struct {
	constraints []memorydomain.MemoryConstraint
}

// TestBuildConstraintSection 测试约束部分构建
func TestBuildConstraintSection(t *testing.T) {
	t.Run("no constraints", func(t *testing.T) {
		mockService := &constraintFixture{
			constraints: []memorydomain.MemoryConstraint{},
		}

		section := ConstraintSection(mockService.constraints)

		assert.Empty(t, section)
	})

	t.Run("preferred name constraint", func(t *testing.T) {
		mockService := &constraintFixture{
			constraints: []memorydomain.MemoryConstraint{
				{
					SubjectID: "456",
					Type:      "preferred_name",
					Value:     "老张",
				},
			},
		}

		section := ConstraintSection(mockService.constraints)

		assert.Contains(t, section, "记忆约束")
		assert.Contains(t, section, "称呼方式")
		assert.Contains(t, section, "User456")
		assert.Contains(t, section, "老张")
		assert.Contains(t, section, "必须使用这些称呼")
	})

	t.Run("allow mention false", func(t *testing.T) {
		mockService := &constraintFixture{
			constraints: []memorydomain.MemoryConstraint{
				{
					SubjectID: "456",
					Type:      "allow_mention",
					Value:     "false",
				},
			},
		}

		section := ConstraintSection(mockService.constraints)

		assert.Contains(t, section, "禁止 @")
		assert.Contains(t, section, "User456")
	})

	t.Run("allow poke false", func(t *testing.T) {
		mockService := &constraintFixture{
			constraints: []memorydomain.MemoryConstraint{
				{
					SubjectID: "456",
					Type:      "allow_poke",
					Value:     "false",
				},
			},
		}

		section := ConstraintSection(mockService.constraints)

		assert.Contains(t, section, "禁止戳一戳")
		assert.Contains(t, section, "User456")
	})

	t.Run("temporary constraint", func(t *testing.T) {
		validUntil := time.Now().Add(1 * time.Hour)

		mockService := &constraintFixture{
			constraints: []memorydomain.MemoryConstraint{
				{
					SubjectID:  "456",
					Type:       "allow_mention",
					Value:      "false",
					ValidUntil: &validUntil,
				},
			},
		}

		section := ConstraintSection(mockService.constraints)

		assert.Contains(t, section, "临时")
		assert.Contains(t, section, "User456")
	})

	t.Run("multiple constraints", func(t *testing.T) {
		mockService := &constraintFixture{
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

		section := ConstraintSection(mockService.constraints)

		assert.Contains(t, section, "记忆约束")
		assert.Contains(t, section, "老张")
		assert.Contains(t, section, "小李")
		assert.Contains(t, section, "禁止 @")
	})
}
