package planning

import (
	"context"
	"fmt"
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
)

// ResponsePlanner 根据决策创建结构化回复计划
type ResponsePlanner struct {
	personaConfig personadomain.PersonaConfig
	composer      TextComposer
}

// TextComposer 生成回复文本的接口
type TextComposer interface {
	ComposeResponse(
		ctx context.Context,
		personaCtx *personadomain.PersonaContext,
		evt *conversationdomain.ConversationEvent,
		intent string,
	) (string, error)
}

// NewResponsePlanner 创建回复计划器
func NewResponsePlanner(
	personaConfig personadomain.PersonaConfig,
	composer TextComposer,
) *ResponsePlanner {
	return &ResponsePlanner{
		personaConfig: personaConfig,
		composer:      composer,
	}
}

// CreateResponsePlan 根据决策和事件创建回复计划
func (p *ResponsePlanner) CreateResponsePlan(
	ctx context.Context,
	decision *presencedomain.ParticipationDecision,
	evt *conversationdomain.ConversationEvent,
	personaCtx *personadomain.PersonaContext,
) (*presencedomain.ResponsePlan, error) {
	// 根据 intent 选择策略
	switch decision.Intent {
	case "respond":
		return p.createDirectResponse(ctx, decision, evt, personaCtx)
	case "continue":
		return p.createContinuation(ctx, decision, evt, personaCtx)
	case "moderate":
		return p.createModeration(ctx, decision, evt, personaCtx)
	case "inform":
		return p.createInformative(ctx, decision, evt, personaCtx)
	default:
		return p.createDirectResponse(ctx, decision, evt, personaCtx)
	}
}

// createDirectResponse 创建直接回答计划
func (p *ResponsePlanner) createDirectResponse(
	ctx context.Context,
	decision *presencedomain.ParticipationDecision,
	evt *conversationdomain.ConversationEvent,
	personaCtx *personadomain.PersonaContext,
) (*presencedomain.ResponsePlan, error) {
	// 生成回复文本
	text, err := p.composer.ComposeResponse(ctx, personaCtx, evt, "direct_answer")
	if err != nil {
		return nil, fmt.Errorf("compose response: %w", err)
	}

	plan := &presencedomain.ResponsePlan{
		PlanID:       generatePlanID(),
		DecisionID:   decision.DecisionID,
		GroupID:      decision.GroupID,
		TargetUserID: decision.TargetUserID,
		CreatedAt:    time.Now(),
		PrimaryAction: presencedomain.ActionPlan{
			ActionType:    "speak",
			Text:          text,
			Intent:        "direct_answer",
		},
		SocialGoal:      "provide_help",
		ExpectedOutcome: "question_answered",
		RiskLevel:       riskLevelFromScore(decision.InterruptionRisk),
	}

	return plan, nil
}

// createContinuation 创建话题延续计划
func (p *ResponsePlanner) createContinuation(
	ctx context.Context,
	decision *presencedomain.ParticipationDecision,
	evt *conversationdomain.ConversationEvent,
	personaCtx *personadomain.PersonaContext,
) (*presencedomain.ResponsePlan, error) {
	text, err := p.composer.ComposeResponse(ctx, personaCtx, evt, "continue_topic")
	if err != nil {
		return nil, fmt.Errorf("compose continuation: %w", err)
	}

	plan := &presencedomain.ResponsePlan{
		PlanID:       generatePlanID(),
		DecisionID:   decision.DecisionID,
		GroupID:      decision.GroupID,
		TargetUserID: decision.TargetUserID,
		CreatedAt:    time.Now(),
		PrimaryAction: presencedomain.ActionPlan{
			ActionType:    "speak",
			Text:          text,
			Intent:        "continue_topic",
		},
		SocialGoal:      "maintain_engagement",
		ExpectedOutcome: "conversation_continues",
		RiskLevel:       riskLevelFromScore(decision.InterruptionRisk),
	}

	return plan, nil
}

// createModeration 创建调解计划
func (p *ResponsePlanner) createModeration(
	ctx context.Context,
	decision *presencedomain.ParticipationDecision,
	evt *conversationdomain.ConversationEvent,
	personaCtx *personadomain.PersonaContext,
) (*presencedomain.ResponsePlan, error) {
	text, err := p.composer.ComposeResponse(ctx, personaCtx, evt, "moderate")
	if err != nil {
		return nil, fmt.Errorf("compose moderation: %w", err)
	}

	plan := &presencedomain.ResponsePlan{
		PlanID:       generatePlanID(),
		DecisionID:   decision.DecisionID,
		GroupID:      decision.GroupID,
		TargetUserID: decision.TargetUserID,
		CreatedAt:    time.Now(),
		PrimaryAction: presencedomain.ActionPlan{
			ActionType:    "speak",
			Text:          text,
			Intent:        "moderate",
		},
		SocialGoal:      "defuse_tension",
		ExpectedOutcome: "conflict_reduced",
		RiskLevel:       "medium",
	}

	return plan, nil
}

// createInformative 创建信息提供计划
func (p *ResponsePlanner) createInformative(
	ctx context.Context,
	decision *presencedomain.ParticipationDecision,
	evt *conversationdomain.ConversationEvent,
	personaCtx *personadomain.PersonaContext,
) (*presencedomain.ResponsePlan, error) {
	text, err := p.composer.ComposeResponse(ctx, personaCtx, evt, "inform")
	if err != nil {
		return nil, fmt.Errorf("compose informative: %w", err)
	}

	plan := &presencedomain.ResponsePlan{
		PlanID:       generatePlanID(),
		DecisionID:   decision.DecisionID,
		GroupID:      decision.GroupID,
		TargetUserID: decision.TargetUserID,
		CreatedAt:    time.Now(),
		PrimaryAction: presencedomain.ActionPlan{
			ActionType:    "speak",
			Text:          text,
			Intent:        "inform",
		},
		SocialGoal:      "share_knowledge",
		ExpectedOutcome: "information_shared",
		RiskLevel:       riskLevelFromScore(decision.InterruptionRisk),
	}

	return plan, nil
}

// 辅助函数

func generatePlanID() string {
	return fmt.Sprintf("plan_%d_%s", time.Now().UnixNano(), randomString(6))
}

func riskLevelFromScore(score float64) string {
	if score < 0.3 {
		return "low"
	} else if score < 0.6 {
		return "medium"
	} else {
		return "high"
	}
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
	}
	return string(b)
}
