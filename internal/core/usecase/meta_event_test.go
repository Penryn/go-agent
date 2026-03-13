package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/core/usecase"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	actionsvc "github.com/phlin/go-agent/internal/services/action"
	autonomysvc "github.com/phlin/go-agent/internal/services/autonomy"
	contextsvc "github.com/phlin/go-agent/internal/services/context"
	gatesvc "github.com/phlin/go-agent/internal/services/gate"
	normalizersvc "github.com/phlin/go-agent/internal/services/normalizer"
	policysvc "github.com/phlin/go-agent/internal/services/policy"
)

type stubPlanner struct{}

func (stubPlanner) Plan(_ context.Context, _ conversationdomain.ContextSnapshot, _ policydomain.AutonomyDecision) (replydomain.ReplyPlan, error) {
	return replydomain.ReplyPlan{}, nil
}

func TestProcessorGroupWhitelistFilters(t *testing.T) {
	store := inmemory.NewStore()
	cfg := config.Default()
	cfg.DefaultPolicy.QuietHours = nil
	normalizer := normalizersvc.New("onebot", cfg.QQ.SelfID, cfg.Persona.Aliases)
	policyService := policysvc.New(cfg)
	contextService := contextsvc.New(store, store, store, policyService, cfg.Persona)
	gateService := gatesvc.New(modeladapter.StaticFactory{})
	autonomyService := autonomysvc.New(policyService, gateService)

	t.Run("non-whitelisted group is filtered", func(t *testing.T) {
		processor := usecase.NewProcessor(
			normalizer,
			contextService,
			autonomyService,
			stubPlanner{}, actionsvc.New(inmemory.NewSender(), nil),
			store, store,
			nil, nil, nil, nil,
			[]int64{100, 200},
		)

		result, err := processor.ProcessEnvelope(context.Background(), conversationdomain.EventEnvelope{
			TraceID: "test-filtered",
			Event: conversationdomain.ConversationEvent{
				Kind:    conversationdomain.EventMessage,
				GroupID: 300,
			},
		})
		if err != nil {
			t.Fatalf("process envelope: %v", err)
		}
		if result.Decision.Action != policydomain.ActionSilent {
			t.Fatalf("expected silent for non-whitelisted group, got %s", result.Decision.Action)
		}
		found := false
		for _, code := range result.Decision.ReasonCodes {
			if code == "group_not_whitelisted" {
				found = true
			}
		}
		if !found {
			t.Fatal("expected group_not_whitelisted reason code")
		}
	})

	t.Run("empty whitelist does not filter", func(t *testing.T) {
		processor := usecase.NewProcessor(
			normalizer,
			contextService,
			autonomyService,
			stubPlanner{}, actionsvc.New(inmemory.NewSender(), nil),
			store, store,
			nil, nil, nil, nil,
			nil,
		)

		result, err := processor.ProcessEnvelope(context.Background(), conversationdomain.EventEnvelope{
			TraceID: "test-no-wl",
			Event: conversationdomain.ConversationEvent{
				Kind:          conversationdomain.EventMessage,
				GroupID:       99999,
				UserID:        1,
				Text:          "hello",
				TimestampUnix: time.Now().Unix(),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, code := range result.Decision.ReasonCodes {
			if code == "group_not_whitelisted" {
				t.Fatal("empty whitelist should allow all groups")
			}
		}
	})

	t.Run("whitelisted group passes through", func(t *testing.T) {
		processor := usecase.NewProcessor(
			normalizer,
			contextService,
			autonomyService,
			stubPlanner{}, actionsvc.New(inmemory.NewSender(), nil),
			store, store,
			nil, nil, nil, nil,
			[]int64{100, 200},
		)

		result, err := processor.ProcessEnvelope(context.Background(), conversationdomain.EventEnvelope{
			TraceID: "test-pass",
			Event: conversationdomain.ConversationEvent{
				Kind:          conversationdomain.EventMessage,
				GroupID:       100,
				UserID:        1,
				Text:          "hello",
				TimestampUnix: time.Now().Unix(),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, code := range result.Decision.ReasonCodes {
			if code == "group_not_whitelisted" {
				t.Fatal("whitelisted group should not be filtered")
			}
		}
	})
}

func TestProcessorSkipsMetaEvents(t *testing.T) {
	store := inmemory.NewStore()
	cfg := config.Default()
	processor := usecase.NewProcessor(
		normalizersvc.New("onebot", cfg.QQ.SelfID, cfg.Persona.Aliases),
		nil,
		nil,
		nil,
		nil,
		store,
		store,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	result, err := processor.ProcessRawEvent(context.Background(), []byte(`{"post_type":"meta_event","meta_event_type":"heartbeat","time":1700000000}`))
	if err != nil {
		t.Fatalf("process raw meta event: %v", err)
	}
	if result.Envelope.Event.Kind != conversationdomain.EventMeta {
		t.Fatalf("unexpected event kind: %s", result.Envelope.Event.Kind)
	}
	if result.Decision.Action != policydomain.ActionSilent {
		t.Fatalf("unexpected decision action: %s", result.Decision.Action)
	}

	events, err := store.RecentEvents(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("recent events: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected meta events to be ignored by archive, got %d events", len(events))
	}

	profile, err := store.GetMemberProfile(context.Background(), 0, 0)
	if err != nil {
		t.Fatalf("get member profile: %v", err)
	}
	if profile.Stats.MessageCount != 0 {
		t.Fatalf("expected meta events to skip profile writes, message_count=%d", profile.Stats.MessageCount)
	}
}
