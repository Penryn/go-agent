package planning

import (
	"context"
	"testing"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
)

// Mock composer
type mockComposer struct {
	text string
	err  error
}

func (m *mockComposer) ComposeResponse(
	ctx context.Context,
	personaCtx *personadomain.PersonaContext,
	evt *conversationdomain.ConversationEvent,
	intent string,
) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	if m.text != "" {
		return m.text, nil
	}
	return "模拟回复: " + intent, nil
}

func TestResponsePlanner_CreateDirectResponse(t *testing.T) {
	composer := &mockComposer{text: "这是直接回答"}
	planner := NewResponsePlanner(personadomain.PersonaConfig{}, composer)

	decision := &presencedomain.ParticipationDecision{
		DecisionID:        "dec_123",
		GroupID:           123456,
		TriggerEventID:    "evt_123",
		Participate:       true,
		ReasonCode:        presencedomain.ReasonDirectMention,
		TargetUserID:      789,
		Intent:            "respond",
		InterruptionRisk:  0.2,
		ConfidenceScore:   0.9,
	}

	evt := &conversationdomain.ConversationEvent{
		EventID:   "evt_123",
		GroupID:   123456,
		UserID:    789,
		MessageID: "msg_123",
		Text:      "@bot 你好",
	}

	personaCtx := &personadomain.PersonaContext{
		Identity: personadomain.PersonaConfig{
			ID:   "test_persona",
			Name: "测试角色",
		},
	}

	plan, err := planner.CreateResponsePlan(context.Background(), decision, evt, personaCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if plan.DecisionID != "dec_123" {
		t.Errorf("expected DecisionID dec_123, got %s", plan.DecisionID)
	}

	if plan.PrimaryAction.ActionType != "speak" {
		t.Errorf("expected ActionType speak, got %s", plan.PrimaryAction.ActionType)
	}

	if plan.PrimaryAction.Text != "这是直接回答" {
		t.Errorf("expected Text '这是直接回答', got %s", plan.PrimaryAction.Text)
	}

	if plan.PrimaryAction.Intent != "direct_answer" {
		t.Errorf("expected Intent direct_answer, got %s", plan.PrimaryAction.Intent)
	}

	if plan.SocialGoal != "provide_help" {
		t.Errorf("expected SocialGoal provide_help, got %s", plan.SocialGoal)
	}

	if plan.RiskLevel != "low" {
		t.Errorf("expected RiskLevel low, got %s", plan.RiskLevel)
	}
}

func TestResponsePlanner_CreateContinuation(t *testing.T) {
	composer := &mockComposer{}
	planner := NewResponsePlanner(personadomain.PersonaConfig{}, composer)

	decision := &presencedomain.ParticipationDecision{
		DecisionID:       "dec_123",
		GroupID:          123456,
		Intent:           "continue",
		InterruptionRisk: 0.4,
	}

	evt := &conversationdomain.ConversationEvent{
		EventID: "evt_123",
		GroupID: 123456,
		Text:    "继续讨论这个话题",
	}

	personaCtx := &personadomain.PersonaContext{}

	plan, err := planner.CreateResponsePlan(context.Background(), decision, evt, personaCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if plan.PrimaryAction.Intent != "continue_topic" {
		t.Errorf("expected Intent continue_topic, got %s", plan.PrimaryAction.Intent)
	}

	if plan.SocialGoal != "maintain_engagement" {
		t.Errorf("expected SocialGoal maintain_engagement, got %s", plan.SocialGoal)
	}

	if plan.RiskLevel != "medium" {
		t.Errorf("expected RiskLevel medium, got %s", plan.RiskLevel)
	}
}

func TestResponsePlanner_CreateModeration(t *testing.T) {
	composer := &mockComposer{}
	planner := NewResponsePlanner(personadomain.PersonaConfig{}, composer)

	decision := &presencedomain.ParticipationDecision{
		DecisionID:       "dec_123",
		GroupID:          123456,
		Intent:           "moderate",
		InterruptionRisk: 0.5,
	}

	evt := &conversationdomain.ConversationEvent{
		EventID: "evt_123",
		GroupID: 123456,
		Text:    "争吵中",
	}

	personaCtx := &personadomain.PersonaContext{}

	plan, err := planner.CreateResponsePlan(context.Background(), decision, evt, personaCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if plan.PrimaryAction.Intent != "moderate" {
		t.Errorf("expected Intent moderate, got %s", plan.PrimaryAction.Intent)
	}

	if plan.SocialGoal != "defuse_tension" {
		t.Errorf("expected SocialGoal defuse_tension, got %s", plan.SocialGoal)
	}
}

func TestResponsePlanner_RiskLevelMapping(t *testing.T) {
	tests := []struct {
		risk     float64
		expected string
	}{
		{0.1, "low"},
		{0.29, "low"},
		{0.3, "medium"},
		{0.5, "medium"},
		{0.59, "medium"},
		{0.6, "high"},
		{0.9, "high"},
	}

	for _, tt := range tests {
		result := riskLevelFromScore(tt.risk)
		if result != tt.expected {
			t.Errorf("risk %f: expected %s, got %s", tt.risk, tt.expected, result)
		}
	}
}
