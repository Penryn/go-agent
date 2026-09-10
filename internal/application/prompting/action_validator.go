package prompting

import (
	"context"
)

// ActionValidator 动作校验器
type ActionValidator struct {
	constraintIntegration *MemoryConstraintIntegration
}

// NewActionValidator 创建动作校验器
func NewActionValidator(integration *MemoryConstraintIntegration) *ActionValidator {
	return &ActionValidator{
		constraintIntegration: integration,
	}
}

// ValidateBeforeSend 发送前校验
func (v *ActionValidator) ValidateBeforeSend(
	ctx context.Context,
	groupID int64,
	action ActionIntent,
) error {
	if v.constraintIntegration == nil {
		return nil
	}

	// 提取所有相关用户 ID
	var allUserIDs []int64
	targetUserID := action.TargetUserID

	if targetUserID > 0 {
		allUserIDs = []int64{targetUserID}
	}

	// 根据动作类型校验
	switch action.Type {
	case "mention", "at":
		if targetUserID > 0 {
			return v.constraintIntegration.ValidateConstraints(ctx, groupID, allUserIDs, "mention", targetUserID)
		}

	case "poke", "nudge":
		if targetUserID > 0 {
			return v.constraintIntegration.ValidateConstraints(ctx, groupID, allUserIDs, "poke", targetUserID)
		}
	}

	return nil
}

// ActionIntent 表示即将执行的动作
type ActionIntent struct {
	Type         string // "mention", "poke", "quote_reply", "speak_text"
	TargetUserID int64  // 目标用户 ID
	Content      string // 动作内容
}

// ExtractActionFromToolCall 从工具调用中提取动作意图
func ExtractActionFromToolCall(toolName string, args map[string]interface{}) ActionIntent {
	intent := ActionIntent{
		Type: toolName,
	}

	// 提取目标用户 ID
	if targetUserID, ok := args["target_user_id"].(int64); ok {
		intent.TargetUserID = targetUserID
	} else if targetUserID, ok := args["user_id"].(int64); ok {
		intent.TargetUserID = targetUserID
	}

	// 提取内容
	if content, ok := args["content"].(string); ok {
		intent.Content = content
	} else if text, ok := args["text"].(string); ok {
		intent.Content = text
	}

	return intent
}

// ValidateToolCall 校验工具调用是否违反约束
func (v *ActionValidator) ValidateToolCall(
	ctx context.Context,
	groupID int64,
	toolName string,
	args map[string]interface{},
) error {
	intent := ExtractActionFromToolCall(toolName, args)
	return v.ValidateBeforeSend(ctx, groupID, intent)
}

// Example usage:
// validator := NewActionValidator(constraintIntegration)
// err := validator.ValidateToolCall(ctx, groupID, "poke_user", map[string]interface{}{"user_id": 123})
// if err != nil {
//     // 违反约束，拒绝执行
//     return fmt.Errorf("action blocked: %w", err)
// }
