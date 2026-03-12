package gate

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"

	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
)

func TestEvaluateStructuredOutput(t *testing.T) {
	service := New(modeladapter.StaticFactory{
		GateModel: modeladapter.NewMockChatModel(schema.AssistantMessage(`{"cue_bot":true,"natural_hook":true,"score":0.9,"reason":"named_bot"}`, nil)),
	})

	decision, err := service.Evaluate(context.Background(), conversationdomain.ContextSnapshot{
		Event: conversationdomain.ConversationEvent{Text: "bot 你怎么看"},
		GroupPolicy: policydomain.GroupPolicy{
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatalf("evaluate gate: %v", err)
	}
	if !decision.CueBot || decision.Score != 0.9 {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestEvaluateClampsScoreAboveOne(t *testing.T) {
	service := New(modeladapter.StaticFactory{
		GateModel: modeladapter.NewMockChatModel(schema.AssistantMessage(`{"cue_bot":true,"score":5.0,"reason":"overflow"}`, nil)),
	})

	decision, err := service.Evaluate(context.Background(), conversationdomain.ContextSnapshot{
		Event: conversationdomain.ConversationEvent{Text: "hello"},
	})
	if err != nil {
		t.Fatalf("evaluate gate: %v", err)
	}
	if decision.Score != 1.0 {
		t.Fatalf("expected score clamped to 1.0, got %f", decision.Score)
	}
}

func TestEvaluateClampsNegativeScore(t *testing.T) {
	service := New(modeladapter.StaticFactory{
		GateModel: modeladapter.NewMockChatModel(schema.AssistantMessage(`{"score":-0.5,"reason":"negative"}`, nil)),
	})

	decision, err := service.Evaluate(context.Background(), conversationdomain.ContextSnapshot{
		Event: conversationdomain.ConversationEvent{Text: "hello"},
	})
	if err != nil {
		t.Fatalf("evaluate gate: %v", err)
	}
	if decision.Score != 0.0 {
		t.Fatalf("expected score clamped to 0.0, got %f", decision.Score)
	}
}
