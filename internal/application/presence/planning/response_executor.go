package planning

import (
	"context"
	"fmt"
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

// ResponseExecutor 执行回复计划
type ResponseExecutor struct {
	outbound OutboundSender
}

// OutboundSender 发送消息的接口
type OutboundSender interface {
	Send(ctx context.Context, action replydomain.ActionExecution) (replydomain.ActionReceipt, error)
}

// NewResponseExecutor 创建回复执行器
func NewResponseExecutor(outbound OutboundSender) *ResponseExecutor {
	return &ResponseExecutor{
		outbound: outbound,
	}
}

// ExecutePlan 执行回复计划
func (e *ResponseExecutor) ExecutePlan(
	ctx context.Context,
	plan *presencedomain.ResponsePlan,
) (actionID string, err error) {
	// 执行主要动作
	actionID, err = e.executeAction(ctx, plan.PrimaryAction, plan)
	if err != nil {
		return "", fmt.Errorf("execute primary action: %w", err)
	}

	// 执行次要动作（如果有）
	for i, action := range plan.SecondaryActions {
		if _, err := e.executeAction(ctx, action, plan); err != nil {
			// 次要动作失败不阻塞主流程
			fmt.Printf("secondary action %d failed: %v\n", i, err)
		}
	}

	return actionID, nil
}

// executeAction 执行单个动作
func (e *ResponseExecutor) executeAction(
	ctx context.Context,
	action presencedomain.ActionPlan,
	plan *presencedomain.ResponsePlan,
) (string, error) {
	switch action.ActionType {
	case "speak":
		return e.sendTextMessage(ctx, plan.GroupID, action)
	case "react":
		return e.sendReaction(ctx, plan.GroupID, action)
	case "meme":
		return e.sendMeme(ctx, plan.GroupID, action)
	default:
		return "", fmt.Errorf("unknown action type: %s", action.ActionType)
	}
}

// sendTextMessage 发送文本消息
func (e *ResponseExecutor) sendTextMessage(
	ctx context.Context,
	groupID int64,
	action presencedomain.ActionPlan,
) (string, error) {
	// 构建文本段
	segments := []conversationdomain.MessageSegment{
		{Type: "text", Data: map[string]any{"text": action.Text}},
	}

	execution := replydomain.ActionExecution{
		ActionID:         generateActionID(),
		Kind:             policydomain.ActionReply,
		GroupID:          groupID,
		Segments:         segments,
	}

	receipt, err := e.outbound.Send(ctx, execution)
	if err != nil {
		return "", err
	}

	return receipt.PlatformMessageID, nil
}

// sendReaction 发送表情回应
func (e *ResponseExecutor) sendReaction(
	ctx context.Context,
	groupID int64,
	action presencedomain.ActionPlan,
) (string, error) {
	segments := []conversationdomain.MessageSegment{
		{Type: "text", Data: map[string]any{"text": action.Emoji}},
	}

	execution := replydomain.ActionExecution{
		ActionID:         generateActionID(),
		Kind:             policydomain.ActionReply,
		GroupID:          groupID,
		Segments:         segments,
	}

	receipt, err := e.outbound.Send(ctx, execution)
	if err != nil {
		return "", err
	}

	return receipt.PlatformMessageID, nil
}

// sendMeme 发送表情包
func (e *ResponseExecutor) sendMeme(
	ctx context.Context,
	groupID int64,
	action presencedomain.ActionPlan,
) (string, error) {
	// 表情包作为图片段发送
	segments := []conversationdomain.MessageSegment{
		{Type: "image", Data: map[string]any{"file": action.MemeAssetID}},
	}

	execution := replydomain.ActionExecution{
		ActionID: generateActionID(),
		Kind:     policydomain.ActionMemeOnly,
		GroupID:  groupID,
		Segments: segments,
	}

	receipt, err := e.outbound.Send(ctx, execution)
	if err != nil {
		return "", err
	}

	return receipt.PlatformMessageID, nil
}

func generateActionID() string {
	return fmt.Sprintf("act_%d_%s", time.Now().UnixNano(), randomString(6))
}

