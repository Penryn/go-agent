package usecase_test

import (
	"context"
	"testing"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/core/usecase"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	normalizersvc "github.com/phlin/go-agent/internal/services/normalizer"
)

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
