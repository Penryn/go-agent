package turn_test

import (
	"context"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/core/ports"
	"github.com/phlin/go-agent/internal/core/usecase"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	turnruntime "github.com/phlin/go-agent/internal/runtime/turn"
	actionsvc "github.com/phlin/go-agent/internal/services/action"
	autonomysvc "github.com/phlin/go-agent/internal/services/autonomy"
	contextsvc "github.com/phlin/go-agent/internal/services/context"
	gatesvc "github.com/phlin/go-agent/internal/services/gate"
	normalizersvc "github.com/phlin/go-agent/internal/services/normalizer"
	policysvc "github.com/phlin/go-agent/internal/services/policy"
	promptingsvc "github.com/phlin/go-agent/internal/services/prompting"
)

func newScenarioRuntime(t *testing.T, whitelist []int64) (*turnruntime.Runtime, *inmemory.Sender) {
	t.Helper()
	store := inmemory.NewStore()
	cfg := config.Default()
	cfg.DefaultPolicy.QuietHours = nil
	cfg.Autonomy.LLMGateEnabled = false
	normalizer := normalizersvc.New("onebot", cfg.QQ.SelfID, cfg.Persona.Aliases)
	policyService := policysvc.New(cfg)
	contextService := contextsvc.New(store, ports.NoopVectorStore{}, store, store, policyService, cfg.Persona)
	gateService := gatesvc.New(modeladapter.StaticFactory{})
	autonomyService := autonomysvc.New(policyService, gateService)
	sender := inmemory.NewSender()
	processor := usecase.NewProcessor(
		normalizer,
		contextService,
		autonomyService,
		promptingsvc.NewDeterministicPlanner(cfg.Persona),
		actionsvc.New(sender, nil, nil),
		store,
		store,
		whitelist,
		cfg.Persona.ID,
	)
	return turnruntime.New(processor), sender
}

func TestRuntimeTurnScenarios(t *testing.T) {
	tests := []struct {
		name           string
		event          conversationdomain.ConversationEvent
		whitelist      []int64
		expectedAction policydomain.DecisionAction
		expectedDrop   string
		expectedSent   bool
	}{
		{
			name: "direct mention replies",
			event: conversationdomain.ConversationEvent{
				EventID:       "mention-1",
				GroupID:       100,
				UserID:        7,
				Kind:          conversationdomain.EventMessage,
				Text:          "艾莲酱在吗",
				MentionedBot:  true,
				TimestampUnix: time.Now().Unix(),
			},
			expectedAction: policydomain.ActionReply,
			expectedSent:   true,
		},
		{
			name: "ordinary low signal stays silent",
			event: conversationdomain.ConversationEvent{
				EventID:       "ordinary-1",
				GroupID:       100,
				UserID:        7,
				Kind:          conversationdomain.EventMessage,
				TimestampUnix: time.Now().Unix(),
			},
			expectedAction: policydomain.ActionSilent,
			expectedDrop:   "action_silent",
		},
		{
			name: "meta event is ignored",
			event: conversationdomain.ConversationEvent{
				EventID: "meta-1",
				GroupID: 0,
				Kind:    conversationdomain.EventMeta,
			},
			expectedAction: policydomain.ActionSilent,
		},
		{
			name: "non-whitelisted group is silent",
			event: conversationdomain.ConversationEvent{
				EventID:       "filtered-1",
				GroupID:       300,
				UserID:        7,
				Kind:          conversationdomain.EventMessage,
				Text:          "艾莲酱在吗",
				MentionedBot:  true,
				TimestampUnix: time.Now().Unix(),
			},
			whitelist:      []int64{100},
			expectedAction: policydomain.ActionSilent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime, sender := newScenarioRuntime(t, tt.whitelist)
			outcome, err := runtime.ProcessEnvelope(context.Background(), conversationdomain.EventEnvelope{
				TraceID: "trace-" + tt.event.EventID,
				Event:   tt.event,
			})
			if err != nil {
				t.Fatalf("process envelope: %v", err)
			}
			if outcome.Decision.Action != tt.expectedAction {
				t.Fatalf("expected decision %q, got %q", tt.expectedAction, outcome.Decision.Action)
			}
			if outcome.Receipt.Sent != tt.expectedSent {
				t.Fatalf("expected sent=%v, got %+v", tt.expectedSent, outcome.Receipt)
			}
			if tt.expectedDrop != "" && outcome.Receipt.DropReason != tt.expectedDrop {
				t.Fatalf("expected drop reason %q, got %q", tt.expectedDrop, outcome.Receipt.DropReason)
			}
			if tt.expectedSent && len(outcome.Plan.Bubbles) == 0 {
				t.Fatal("expected a reply bubble")
			}
			if tt.expectedSent && len(sender.Actions()) != 1 {
				t.Fatalf("expected one outbound action, got %d", len(sender.Actions()))
			}
			if !tt.expectedSent && len(sender.Actions()) != 0 {
				t.Fatalf("expected no outbound actions, got %d", len(sender.Actions()))
			}
		})
	}
}
