// Package perception runs slow media understanding outside the ingress and
// actor paths, then returns results to the owning group actor as enrichment.
package perception

import (
	"context"
	"log/slog"
	"strings"
	"time"

	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
	groupactor "github.com/phlin/go-agent/internal/humanbot/runtime/group_actor"
	backgroundruntime "github.com/phlin/go-agent/internal/runtime/background"
	memesvc "github.com/phlin/go-agent/internal/services/meme"
	multimodalsvc "github.com/phlin/go-agent/internal/services/multimodal"
)

type backgroundSubmitter interface {
	Submit(backgroundruntime.Job) bool
}

type Pipeline struct {
	vision     *multimodalsvc.Service
	memes      *memesvc.Service
	working    *groupactor.Manager
	background backgroundSubmitter
	timeout    time.Duration
}

func New(vision *multimodalsvc.Service, memes *memesvc.Service, working *groupactor.Manager, background backgroundSubmitter) *Pipeline {
	return &Pipeline{vision: vision, memes: memes, working: working, background: background, timeout: 45 * time.Second}
}

// Submit never blocks message ingress. Queue pressure can defer media
// understanding, but the original event has already been persisted by Actor.
func (p *Pipeline) Submit(record humandomain.EventRecord) {
	if p == nil || p.background == nil || p.working == nil || record.Origin != humandomain.OriginInbound || len(record.Event.Attachments) == 0 {
		return
	}
	record.RawPayload = nil
	if !p.background.Submit(backgroundruntime.Job{
		Name:    "media_perception",
		Timeout: p.timeout,
		Run: func(ctx context.Context) error {
			return p.process(ctx, record)
		},
	}) {
		slog.Warn("perception: job dropped", "group_id", record.GroupID, "event_id", record.EventID)
	}
}

func (p *Pipeline) process(ctx context.Context, record humandomain.EventRecord) error {
	descriptors := fallbackDescriptors(record.Event.Attachments)
	if p.vision != nil {
		result, err := p.vision.Understand(ctx, record.Event.Attachments)
		if err != nil {
			slog.Warn("perception: vision failed, using fallback", "event_id", record.EventID, "err", err)
		} else if len(result) > 0 {
			descriptors = result
		}
	}
	if err := p.working.EnrichMedia(ctx, record.GroupID, record.EventID, descriptors); err != nil {
		return err
	}
	if p.memes != nil && hasSticker(record.Event.Attachments) {
		stickerEvent := record.Event
		stickerEvent.Attachments = stickersOnly(record.Event.Attachments)
		if err := p.memes.ObserveEvent(ctx, stickerEvent, descriptors); err != nil {
			return err
		}
	}
	return nil
}

func hasSticker(attachments []mediadomain.MultimodalAttachment) bool {
	for _, attachment := range attachments {
		if attachment.Kind == mediadomain.MediaSticker {
			return true
		}
	}
	return false
}

func stickersOnly(attachments []mediadomain.MultimodalAttachment) []mediadomain.MultimodalAttachment {
	stickers := make([]mediadomain.MultimodalAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		if attachment.Kind == mediadomain.MediaSticker {
			stickers = append(stickers, attachment)
		}
	}
	return stickers
}

func fallbackDescriptors(attachments []mediadomain.MultimodalAttachment) []mediadomain.MediaDescriptor {
	descriptors := make([]mediadomain.MediaDescriptor, 0, len(attachments))
	for _, attachment := range attachments {
		summary := strings.TrimSpace(attachment.PlatformHint)
		if summary == "" {
			summary = "收到一个" + string(attachment.Kind) + "附件"
		}
		descriptors = append(descriptors, mediadomain.MediaDescriptor{
			AttachmentID: attachment.AttachmentID,
			Kind:         attachment.Kind,
			Summary:      summary,
			Confidence:   0.2,
		})
	}
	return descriptors
}
