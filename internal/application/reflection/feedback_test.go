package reflection

import (
	"testing"
	"time"

	feedbackdomain "github.com/phlin/go-agent/internal/domain/feedback"
)

func TestFeedbackClassifier_NoResponse(t *testing.T) {
	classifier := NewFeedbackClassifier(nil)

	feedback, err := classifier.ClassifyFeedback("action_123", []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if feedback.Type != feedbackdomain.TypeIgnored {
		t.Errorf("expected Type %s, got %s", feedbackdomain.TypeIgnored, feedback.Type)
	}

	if feedback.EngagementLevel != 0.0 {
		t.Errorf("expected EngagementLevel 0.0, got %f", feedback.EngagementLevel)
	}

	if len(feedback.Signals) != 1 {
		t.Errorf("expected 1 signal, got %d", len(feedback.Signals))
	}

	if feedback.Signals[0].SignalType != feedbackdomain.SignalNoResponse {
		t.Errorf("expected signal %s, got %s", feedbackdomain.SignalNoResponse, feedback.Signals[0].SignalType)
	}
}

func TestFeedbackClassifier_SingleResponse(t *testing.T) {
	classifier := NewFeedbackClassifier(nil)

	feedback, err := classifier.ClassifyFeedback("action_123", []string{"evt_1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if feedback.Type != feedbackdomain.TypeNeutral {
		t.Errorf("expected Type %s, got %s", feedbackdomain.TypeNeutral, feedback.Type)
	}

	if feedback.EngagementLevel < 0.2 || feedback.EngagementLevel > 0.4 {
		t.Errorf("expected EngagementLevel around 0.3, got %f", feedback.EngagementLevel)
	}

	if len(feedback.ObservedEventIDs) != 1 {
		t.Errorf("expected 1 observed event, got %d", len(feedback.ObservedEventIDs))
	}
}

func TestFeedbackClassifier_ModerateResponse(t *testing.T) {
	classifier := NewFeedbackClassifier(nil)

	feedback, err := classifier.ClassifyFeedback("action_123", []string{"evt_1", "evt_2", "evt_3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if feedback.Type != feedbackdomain.TypeNeutral {
		t.Errorf("expected Type %s, got %s", feedbackdomain.TypeNeutral, feedback.Type)
	}

	if feedback.EngagementLevel < 0.4 || feedback.EngagementLevel > 0.6 {
		t.Errorf("expected EngagementLevel around 0.5, got %f", feedback.EngagementLevel)
	}

	if len(feedback.ObservedEventIDs) != 3 {
		t.Errorf("expected 3 observed events, got %d", len(feedback.ObservedEventIDs))
	}
}

func TestFeedbackClassifier_ActiveResponse(t *testing.T) {
	classifier := NewFeedbackClassifier(nil)

	eventIDs := []string{"evt_1", "evt_2", "evt_3", "evt_4", "evt_5", "evt_6"}
	feedback, err := classifier.ClassifyFeedback("action_123", eventIDs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if feedback.Type != feedbackdomain.TypePositive {
		t.Errorf("expected Type %s, got %s", feedbackdomain.TypePositive, feedback.Type)
	}

	if feedback.EngagementLevel < 0.7 {
		t.Errorf("expected high EngagementLevel, got %f", feedback.EngagementLevel)
	}

	if feedback.OverallSentiment <= 0 {
		t.Errorf("expected positive sentiment, got %f", feedback.OverallSentiment)
	}

	// 应该包含 continued 信号
	hasContinued := false
	for _, signal := range feedback.Signals {
		if signal.SignalType == feedbackdomain.SignalContinued {
			hasContinued = true
			break
		}
	}
	if !hasContinued {
		t.Error("expected SignalContinued in signals")
	}
}

func TestFeedbackWindow(t *testing.T) {
	collector := NewFeedbackCollector(nil, nil)

	window, err := collector.StartFeedbackWindow(
		nil,
		"dec_123",
		"act_456",
		123456,
		testNow(),
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if window.DecisionID != "dec_123" {
		t.Errorf("expected DecisionID dec_123, got %s", window.DecisionID)
	}

	if window.ActionID != "act_456" {
		t.Errorf("expected ActionID act_456, got %s", window.ActionID)
	}

	if window.Status != "observing" {
		t.Errorf("expected Status observing, got %s", window.Status)
	}

	if window.ObserveDuration.Seconds() != 30 {
		t.Errorf("expected ObserveDuration 30s, got %v", window.ObserveDuration)
	}

	if window.MaxEvents != 10 {
		t.Errorf("expected MaxEvents 10, got %d", window.MaxEvents)
	}
}

func testNow() time.Time {
	return time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
}
