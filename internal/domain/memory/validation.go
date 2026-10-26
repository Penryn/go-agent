package memory

import (
	"fmt"
	"time"
)

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
