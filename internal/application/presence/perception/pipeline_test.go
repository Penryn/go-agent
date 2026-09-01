package perception

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	memesvc "github.com/phlin/go-agent/internal/application/meme"
	groupactor "github.com/phlin/go-agent/internal/application/presence/group_actor"
	"github.com/phlin/go-agent/internal/application/presence/ingress"
	"github.com/phlin/go-agent/internal/config"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
)

type recordingOutbox struct {
	kind string
	key  string
	body []byte
}

func (o *recordingOutbox) Enqueue(_ context.Context, kind, key string, body []byte) error {
	o.kind, o.key, o.body = kind, key, append([]byte(nil), body...)
	return nil
}

func TestPipelineEnqueuesDurablePerceptionTask(t *testing.T) {
	working := groupactor.NewManager(ingress.NewMemoryEventLog())
	defer working.Close()
	outbox := &recordingOutbox{}
	pipeline := New(nil, nil, working, WithOutbox(outbox))
	record := presencedomain.EventRecord{
		EventID: "durable-event", GroupID: 42, Origin: presencedomain.OriginInbound,
		Event: conversationdomain.ConversationEvent{
			EventID: "durable-event", GroupID: 42, Kind: conversationdomain.EventMessage,
			Attachments: []mediadomain.MultimodalAttachment{{AttachmentID: "a1", Kind: mediadomain.MediaImage}},
		},
	}
	pipeline.Submit(record)
	if outbox.kind != "perception_event" || outbox.key != record.EventID {
		t.Fatalf("unexpected outbox envelope: kind=%q key=%q", outbox.kind, outbox.key)
	}
	var got presencedomain.EventRecord
	if err := json.Unmarshal(outbox.body, &got); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if got.EventID != record.EventID || len(got.Event.Attachments) != 1 {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

func TestPipelineWithoutVisionSkipsMediaEnrichmentAndCollection(t *testing.T) {
	store := inmemory.NewStore()
	working := groupactor.NewManager(ingress.NewMemoryEventLog())
	defer working.Close()
	pipeline := New(nil, memesvc.New(store, config.MemeConfig{AutoCollect: true}), working)

	record := presencedomain.EventRecord{
		EventID:   "event-1",
		GroupID:   42,
		UserID:    7,
		Origin:    presencedomain.OriginInbound,
		Timestamp: time.Unix(100, 0),
		Event: conversationdomain.ConversationEvent{
			EventID: "event-1",
			GroupID: 42,
			UserID:  7,
			Kind:    conversationdomain.EventMessage,
			Attachments: []mediadomain.MultimodalAttachment{{
				AttachmentID: "sticker-1",
				Kind:         mediadomain.MediaSticker,
				ObjectKey:    "stickers/sticker-1.gif",
				ContentHash:  "content-1",
				PlatformHint: "开心小狗",
			}},
		},
	}
	if _, err := working.Observe(context.Background(), record); err != nil {
		t.Fatalf("observe event: %v", err)
	}

	if err := pipeline.Process(context.Background(), record); err != nil {
		t.Fatalf("process: %v", err)
	}

	// vision 缺席时媒体不被理解：不 enrich、不收藏，
	// 不用 PlatformHint 拼假描述污染下游。
	memory, err := working.Snapshot(context.Background(), record.GroupID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got := memory.MediaByEvent[record.EventID]; len(got) != 0 {
		t.Fatalf("expected no media enrichment without vision, got %+v", got)
	}
	if _, _, err := store.GetMeme(context.Background(), "meme-content-1"); err == nil {
		t.Fatal("sticker should not be collected without vision understanding")
	}
}
