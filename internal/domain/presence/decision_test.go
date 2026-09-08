package presence

import (
	"testing"
	"time"
)

func TestParticipationDecision(t *testing.T) {
	decision := ParticipationDecision{
		DecisionID:       "dec_test_123",
		GroupID:          123456,
		TriggerEventID:   "evt_456",
		DecidedAt:        time.Now(),
		Participate:      true,
		ReasonCode:       ReasonDirectMention,
		TargetUserID:     789,
		Audience:         "target_only",
		Intent:           "respond",
		SocialValue:      0.7,
		InterruptionRisk: 0.2,
		ConfidenceScore:  0.85,
		ExpiresAt:        time.Now().Add(5 * time.Minute),
		RuleHits:         []string{"direct_mention"},
	}

	if !decision.Participate {
		t.Error("expected Participate to be true")
	}

	if decision.ReasonCode != ReasonDirectMention {
		t.Errorf("expected ReasonCode %s, got %s", ReasonDirectMention, decision.ReasonCode)
	}

	if decision.Intent != "respond" {
		t.Errorf("expected Intent 'respond', got %s", decision.Intent)
	}
}

func TestResponsePlan(t *testing.T) {
	plan := ResponsePlan{
		PlanID:       "plan_test_123",
		DecisionID:   "dec_test_123",
		GroupID:      123456,
		TargetUserID: 789,
		CreatedAt:    time.Now(),
		PrimaryAction: ActionPlan{
			ActionType: "speak",
			Text:       "你好！",
			Intent:     "greeting",
		},
		SocialGoal:      "build_rapport",
		ExpectedOutcome: "positive_response",
		RiskLevel:       "low",
	}

	if plan.PrimaryAction.ActionType != "speak" {
		t.Errorf("expected ActionType 'speak', got %s", plan.PrimaryAction.ActionType)
	}

	if plan.SocialGoal != "build_rapport" {
		t.Errorf("expected SocialGoal 'build_rapport', got %s", plan.SocialGoal)
	}
}

func TestFeedbackWindow(t *testing.T) {
	window := FeedbackWindow{
		WindowID:        "win_test_123",
		DecisionID:      "dec_test_123",
		ActionID:        "act_test_456",
		GroupID:         123456,
		SentAt:          time.Now(),
		ObserveDuration: 30 * time.Second,
		MaxEvents:       10,
		Status:          "observing",
		ObservedEventIDs: []string{},
	}

	if window.Status != "observing" {
		t.Errorf("expected Status 'observing', got %s", window.Status)
	}

	if window.ObserveDuration != 30*time.Second {
		t.Errorf("expected ObserveDuration 30s, got %v", window.ObserveDuration)
	}

	// 模拟窗口关闭
	window.Status = "completed"
	window.ClosedAt = time.Now()
	window.FeedbackType = FeedbackPositive
	window.ObservedEventIDs = []string{"evt_1", "evt_2"}

	if window.Status != "completed" {
		t.Error("expected Status to be 'completed'")
	}

	if len(window.ObservedEventIDs) != 2 {
		t.Errorf("expected 2 observed events, got %d", len(window.ObservedEventIDs))
	}
}

func TestReasonCodes(t *testing.T) {
	// 验证常量定义
	codes := []string{
		ReasonBlacklisted,
		ReasonCooldown,
		ReasonConsecutiveLimit,
		ReasonDirectMention,
		ReasonQuestionToBot,
		ReasonModelSilent,
	}

	for _, code := range codes {
		if code == "" {
			t.Error("reason code should not be empty")
		}
	}
}
