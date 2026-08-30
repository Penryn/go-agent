package perception

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	"github.com/phlin/go-agent/internal/config"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
	groupactor "github.com/phlin/go-agent/internal/humanbot/runtime/group_actor"
	"github.com/phlin/go-agent/internal/humanbot/runtime/ingress"
	backgroundruntime "github.com/phlin/go-agent/internal/runtime/background"
	memesvc "github.com/phlin/go-agent/internal/services/meme"
)

type inlineBackground struct{}

func (inlineBackground) Submit(job backgroundruntime.Job) bool {
	backgroundruntime.RunInline(context.Background(), job)
	return true
}

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
	pipeline := New(nil, nil, working, inlineBackground{}, WithOutbox(outbox))
	record := humandomain.EventRecord{
		EventID: "durable-event", GroupID: 42, Origin: humandomain.OriginInbound,
		Event: conversationdomain.ConversationEvent{
			EventID: "durable-event", GroupID: 42, Kind: conversationdomain.EventMessage,
			Attachments: []mediadomain.MultimodalAttachment{{AttachmentID: "a1", Kind: mediadomain.MediaImage}},
		},
	}
	pipeline.Submit(record)
	if outbox.kind != "perception_event" || outbox.key != record.EventID {
		t.Fatalf("unexpected outbox envelope: kind=%q key=%q", outbox.kind, outbox.key)
	}
	var got humandomain.EventRecord
	if err := json.Unmarshal(outbox.body, &got); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if got.EventID != record.EventID || len(got.Event.Attachments) != 1 {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

func TestPipelineFallsBackToPlatformHintAndCollectsSticker(t *testing.T) {
	store := inmemory.NewStore()
	working := groupactor.NewManager(ingress.NewMemoryEventLog())
	defer working.Close()
	pipeline := New(nil, memesvc.New(store, config.MemeConfig{AutoCollect: true}), working, inlineBackground{})

	record := humandomain.EventRecord{
		EventID:   "event-1",
		GroupID:   42,
		UserID:    7,
		Origin:    humandomain.OriginInbound,
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

	pipeline.Submit(record)

	memory, err := working.Snapshot(context.Background(), record.GroupID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	got := memory.MediaByEvent[record.EventID]
	if len(got) != 1 || got[0].Summary != "开心小狗" || got[0].Confidence != 0.2 {
		t.Fatalf("unexpected media enrichment: %+v", got)
	}
	asset, descriptor, err := store.GetMeme(context.Background(), "meme-content-1")
	if err != nil {
		t.Fatalf("get collected sticker: %v", err)
	}
	if asset.ObjectKey != "stickers/sticker-1.gif" || descriptor.Summary != "开心小狗" {
		t.Fatalf("unexpected collected sticker: asset=%+v descriptor=%+v", asset, descriptor)
	}
}
