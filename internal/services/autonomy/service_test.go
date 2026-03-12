package autonomy

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
	"github.com/phlin/go-agent/internal/config"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	gatesvc "github.com/phlin/go-agent/internal/services/gate"
	policysvc "github.com/phlin/go-agent/internal/services/policy"
)

func TestDecideDirectTrigger(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultPolicy.QuietHours = nil
	policySvc := policysvc.New(cfg)
	svc := New(policySvc, nil)

	snapshot := conversationdomain.ContextSnapshot{
		Event: conversationdomain.ConversationEvent{
			GroupID:       1,
			UserID:        2,
			MentionedBot:  true,
			TimestampUnix: time.Now().Unix(),
		},
		GroupPolicy: cfg.DefaultPolicy,
		RuntimeState: policydomain.RuntimeState{
			GroupID: 1,
			State:   policydomain.StateObserving,
		},
	}

	decision, state, err := svc.Decide(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}

	if decision.Action != policydomain.ActionReply {
		t.Fatalf("unexpected decision action: %s", decision.Action)
	}
	if state.State != policydomain.StateCooldown {
		t.Fatalf("unexpected runtime state: %s", state.State)
	}
}

func TestDecideQuietHour(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultPolicy.QuietHours = []string{"00:00-23:59"}
	policySvc := policysvc.New(cfg)
	svc := New(policySvc, nil)

	snapshot := conversationdomain.ContextSnapshot{
		Event: conversationdomain.ConversationEvent{
			GroupID:       1,
			UserID:        2,
			MentionedBot:  true,
			TimestampUnix: time.Now().Unix(),
		},
		GroupPolicy: cfg.DefaultPolicy,
		RuntimeState: policydomain.RuntimeState{
			GroupID: 1,
			State:   policydomain.StateObserving,
		},
	}

	decision, _, err := svc.Decide(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}

	if decision.Action != policydomain.ActionSilent {
		t.Fatalf("expected silent during quiet hours, got %s", decision.Action)
	}
}

func TestDecideWithLLMGate(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultPolicy.QuietHours = nil
	cfg.Autonomy.LLMGateEnabled = true
	cfg.Autonomy.ProactiveScoreThreshold = 0.5
	policySvc := policysvc.New(cfg)
	gateService := gatesvc.New(modeladapter.StaticFactory{
		GateModel: modeladapter.NewMockChatModel(schema.AssistantMessage(`{"cue_bot":false,"natural_hook":true,"score":0.8,"reason":"question_hook"}`, nil)),
	})
	svc := New(policySvc, gateService)

	snapshot := conversationdomain.ContextSnapshot{
		Event: conversationdomain.ConversationEvent{
			GroupID:       1,
			UserID:        2,
			Text:          "你们觉得这个离谱吗？",
			TimestampUnix: time.Now().Unix(),
		},
		GroupPolicy: cfg.DefaultPolicy,
		RuntimeState: policydomain.RuntimeState{
			GroupID: 1,
			State:   policydomain.StateObserving,
		},
	}

	decision, _, err := svc.Decide(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if decision.Action != policydomain.ActionReply {
		t.Fatalf("expected gate to trigger reply, got %s", decision.Action)
	}
}
