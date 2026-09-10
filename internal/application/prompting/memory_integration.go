package prompting

import (
	"context"
	"fmt"
	"strings"

	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

// MemoryConstraintService 获取记忆约束的接口
type MemoryConstraintService interface {
	GetConstraints(ctx context.Context, scope string, targetIDs []string) ([]memorydomain.MemoryConstraint, error)
}

// MemoryConstraintIntegration 记忆约束集成
type MemoryConstraintIntegration struct {
	memoryService MemoryConstraintService
}

// NewMemoryConstraintIntegration 创建约束集成器
func NewMemoryConstraintIntegration(memService MemoryConstraintService) *MemoryConstraintIntegration {
	return &MemoryConstraintIntegration{
		memoryService: memService,
	}
}

// BuildConstraintSection 构建约束部分的指令
func (m *MemoryConstraintIntegration) BuildConstraintSection(ctx context.Context, groupID int64, targetUserIDs []int64) (string, error) {
	if m.memoryService == nil {
		return "", nil
	}

	// 转换为字符串 ID
	scope := fmt.Sprintf("group_%d", groupID)
	targetIDs := make([]string, len(targetUserIDs))
	for i, id := range targetUserIDs {
		targetIDs[i] = fmt.Sprintf("%d", id)
	}

	// 获取约束
	constraints, err := m.memoryService.GetConstraints(ctx, scope, targetIDs)
	if err != nil {
		return "", fmt.Errorf("failed to get constraints: %w", err)
	}

	if len(constraints) == 0 {
		return "", nil
	}

	// 构建指令
	var sections []string
	sections = append(sections, "", "记忆约束（必须遵守）:")

	// 按类型分组
	preferredNames := make(map[string]string)
	allowMentions := make(map[string]bool)
	allowPokes := make(map[string]bool)
	temporaryConstraints := []string{}

	for _, c := range constraints {
		switch c.Type {
		case "preferred_name":
			preferredNames[c.SubjectID] = c.Value

		case "allow_mention":
			if c.Value == "false" {
				allowMentions[c.SubjectID] = false
				if c.IsTemporary() {
					temporaryConstraints = append(temporaryConstraints,
						fmt.Sprintf("User%s: 不允许 @（临时，至 %s）", c.SubjectID, c.ValidUntil.Format("15:04")))
				}
			}

		case "allow_poke":
			if c.Value == "false" {
				allowPokes[c.SubjectID] = false
				if c.IsTemporary() {
					temporaryConstraints = append(temporaryConstraints,
						fmt.Sprintf("User%s: 不允许戳一戳（临时，至 %s）", c.SubjectID, c.ValidUntil.Format("15:04")))
				}
			}
		}
	}

	// 1. 称呼约束
	if len(preferredNames) > 0 {
		names := make([]string, 0, len(preferredNames))
		for userID, name := range preferredNames {
			names = append(names, fmt.Sprintf("User%s=%q", userID, name))
		}
		sections = append(sections, "- 称呼方式: "+strings.Join(names, "；")+"。必须使用这些称呼，不得使用其他昵称。")
	}

	// 2. @ 约束
	if len(allowMentions) > 0 {
		disallowed := []string{}
		for userID, allow := range allowMentions {
			if !allow {
				disallowed = append(disallowed, fmt.Sprintf("User%s", userID))
			}
		}
		if len(disallowed) > 0 {
			sections = append(sections, "- 禁止 @: "+strings.Join(disallowed, "、")+"。如需联系他们，使用私聊或其他方式。")
		}
	}

	// 3. 戳一戳约束
	if len(allowPokes) > 0 {
		disallowed := []string{}
		for userID, allow := range allowPokes {
			if !allow {
				disallowed = append(disallowed, fmt.Sprintf("User%s", userID))
			}
		}
		if len(disallowed) > 0 {
			sections = append(sections, "- 禁止戳一戳: "+strings.Join(disallowed, "、")+"。")
		}
	}

	// 4. 临时约束提示
	if len(temporaryConstraints) > 0 {
		sections = append(sections, "- 临时限制: "+strings.Join(temporaryConstraints, "；")+"。")
	}

	return strings.Join(sections, "\n"), nil
}

// ValidateConstraints 发送前校验（验证即将发送的动作是否违反约束）
func (m *MemoryConstraintIntegration) ValidateConstraints(
	ctx context.Context,
	groupID int64,
	targetUserIDs []int64,
	actionType string,
	targetUserID int64,
) error {
	if m.memoryService == nil {
		return nil
	}

	scope := fmt.Sprintf("group_%d", groupID)
	targetIDs := make([]string, len(targetUserIDs))
	for i, id := range targetUserIDs {
		targetIDs[i] = fmt.Sprintf("%d", id)
	}

	constraints, err := m.memoryService.GetConstraints(ctx, scope, targetIDs)
	if err != nil {
		return fmt.Errorf("failed to get constraints: %w", err)
	}

	targetIDStr := fmt.Sprintf("%d", targetUserID)

	for _, c := range constraints {
		if c.SubjectID != targetIDStr {
			continue
		}

		// 检查是否过期
		if !c.IsValid() {
			continue
		}

		switch actionType {
		case "mention", "at":
			if c.Type == "allow_mention" && c.Value == "false" {
				return fmt.Errorf("不允许 @ User%s（约束：%s）", targetIDStr, c.Type)
			}

		case "poke", "nudge":
			if c.Type == "allow_poke" && c.Value == "false" {
				return fmt.Errorf("不允许戳一戳 User%s（约束：%s）", targetIDStr, c.Type)
			}
		}
	}

	return nil
}
