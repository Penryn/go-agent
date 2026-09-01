// Package perception runs slow media understanding outside the ingress and
// actor paths, then returns results to the owning group actor as enrichment.
package perception

import (
	"context"
	"encoding/json"
	"log/slog"

	memesvc "github.com/phlin/go-agent/internal/application/meme"
	multimodalsvc "github.com/phlin/go-agent/internal/application/multimodal"
	groupactor "github.com/phlin/go-agent/internal/application/presence/group_actor"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
)

type Pipeline struct {
	vision  *multimodalsvc.Service
	memes   *memesvc.Service
	working *groupactor.Manager
	outbox  interface {
		Enqueue(context.Context, string, string, []byte) error
	}
}

type Option func(*Pipeline)

func WithOutbox(submitter interface {
	Enqueue(context.Context, string, string, []byte) error
}) Option {
	return func(p *Pipeline) { p.outbox = submitter }
}

func New(vision *multimodalsvc.Service, memes *memesvc.Service, working *groupactor.Manager, opts ...Option) *Pipeline {
	p := &Pipeline{vision: vision, memes: memes, working: working}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Submit never blocks message ingress. Queue pressure can defer media
// understanding, but the original event has already been persisted by Actor.
func (p *Pipeline) Submit(record presencedomain.EventRecord) {
	if p == nil || p.outbox == nil || p.working == nil || record.Origin != presencedomain.OriginInbound || len(record.Event.Attachments) == 0 {
		return
	}
	record.RawPayload = nil
	payload, err := json.Marshal(record)
	if err == nil {
		err = p.outbox.Enqueue(context.Background(), "perception_event", record.EventID, payload)
	}
	if err != nil {
		slog.Warn("perception: outbox enqueue failed", "event_id", record.EventID, "err", err)
	}
}

// Process executes one persisted perception task. It is the handler seam used
// by the durable outbox runtime.
func (p *Pipeline) Process(ctx context.Context, record presencedomain.EventRecord) error {
	return p.process(ctx, record)
}

func (p *Pipeline) process(ctx context.Context, record presencedomain.EventRecord) error {
	// vision 失败或无结果时 descriptors 为空：媒体没被理解就当没看见，
	// 不用 PlatformHint 拼假描述污染下游（meme 收藏 / 接图判断）。
	var descriptors []mediadomain.MediaDescriptor
	if p.vision != nil {
		result, err := p.vision.Understand(ctx, record.Event.Attachments)
		if err != nil {
			slog.Warn("perception: vision failed", "event_id", record.EventID, "err", err)
		} else {
			descriptors = result
		}
	}
	if err := p.working.EnrichMedia(ctx, record.GroupID, record.EventID, descriptors); err != nil {
		return err
	}
	if p.memes != nil {
		if err := p.memes.ObserveEvent(ctx, record.Event, descriptors); err != nil {
			return err
		}
	}
	return nil
}
