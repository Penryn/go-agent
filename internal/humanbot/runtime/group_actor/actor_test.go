package group_actor

import (
	"context"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
	"github.com/phlin/go-agent/internal/humanbot/runtime/ingress"
)

func TestManagerMergesShortBurstAndKeepsOutboundEvent(t *testing.T) {
	log := ingress.NewMemoryEventLog()
	store := inmemory.NewStore()
	manager := NewManager(log, WithTailSize(4), WithArchive(store))
	defer manager.Close()

	base := time.Unix(100, 0)
	first := eventRecord("e1", 1, 7, "今晚开黑吗", base)
	second := eventRecord("e2", 1, 7, "八点行不行", base.Add(time.Second))
	memory, err := manager.Observe(context.Background(), first)
	if err != nil {
		t.Fatalf("observe first: %v", err)
	}
	memory, err = manager.Observe(context.Background(), second)
	if err != nil {
		t.Fatalf("observe second: %v", err)
	}
	if len(memory.CurrentBurst.EventIDs) != 2 || memory.CurrentBurst.Text != "今晚开黑吗 八点行不行" {
		t.Fatalf("burst was not merged: %+v", memory.CurrentBurst)
	}
	if memory.Candidates[0].Status != humandomain.CandidateCancelled || memory.Candidates[1].Status != humandomain.CandidatePending {
		t.Fatalf("burst candidates were not superseded: %+v", memory.Candidates)
	}
	if ok, err := manager.CanExecute(context.Background(), 1, memory.Candidates[0].CandidateID, base.Add(time.Second)); err != nil || ok {
		t.Fatalf("superseded candidate must not execute: ok=%v err=%v", ok, err)
	}

	outbound := eventRecord("out-1", 1, 999, "那就八点", base.Add(3*time.Second))
	outbound.Origin = humandomain.OriginOutbound
	memory, err = manager.Observe(context.Background(), outbound)
	if err != nil {
		t.Fatalf("observe outbound: %v", err)
	}
	if len(memory.RecentTail) != 3 || memory.CurrentBurst.EventIDs != nil {
		t.Fatalf("outbound event did not settle burst: %+v", memory)
	}
	if _, err := manager.Observe(context.Background(), outbound); err != nil {
		t.Fatalf("observe duplicate outbound: %v", err)
	}
	archived, err := store.RecentEvents(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("read archived events: %v", err)
	}
	if len(archived) != 3 {
		t.Fatalf("duplicate event was archived: %d", len(archived))
	}
}

func TestManagerEnrichesMediaOnlyForRecentEvent(t *testing.T) {
	manager := NewManager(ingress.NewMemoryEventLog())
	defer manager.Close()

	record := eventRecord("media-1", 1, 7, "", time.Unix(100, 0))
	record.Event.Attachments = []mediadomain.MultimodalAttachment{{AttachmentID: "sticker-1", Kind: mediadomain.MediaSticker}}
	if _, err := manager.Observe(context.Background(), record); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if err := manager.EnrichMedia(context.Background(), 1, "media-1", []mediadomain.MediaDescriptor{{AttachmentID: "sticker-1", Kind: mediadomain.MediaSticker, Summary: "开心小狗"}}); err != nil {
		t.Fatalf("enrich media: %v", err)
	}
	memory, err := manager.Snapshot(context.Background(), 1)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got := memory.MediaByEvent["media-1"][0].Summary; got != "开心小狗" {
		t.Fatalf("unexpected media summary: %q", got)
	}
	if err := manager.EnrichMedia(context.Background(), 1, "missing", []mediadomain.MediaDescriptor{{Summary: "ignored"}}); err != nil {
		t.Fatalf("enrich missing event: %v", err)
	}
	memory, err = manager.Snapshot(context.Background(), 1)
	if err != nil {
		t.Fatalf("snapshot after missing enrichment: %v", err)
	}
	if _, ok := memory.MediaByEvent["missing"]; ok {
		t.Fatal("missing event should not have media enrichment")
	}
}

func eventRecord(id string, groupID, userID int64, text string, at time.Time) humandomain.EventRecord {
	return humandomain.EventRecord{
		EventID:   id,
		GroupID:   groupID,
		UserID:    userID,
		Origin:    humandomain.OriginInbound,
		Timestamp: at,
		Event: conversationdomain.ConversationEvent{
			EventID:       id,
			GroupID:       groupID,
			UserID:        userID,
			Kind:          conversationdomain.EventMessage,
			Text:          text,
			TimestampUnix: at.Unix(),
		},
	}
}
