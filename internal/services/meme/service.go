package meme

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
)

type Service struct {
	store ports.MemeStore
}

func New(store ports.MemeStore) *Service {
	return &Service{store: store}
}

func (s *Service) ObserveEvent(ctx context.Context, event conversationdomain.ConversationEvent) error {
	for _, attachment := range event.Attachments {
		if attachment.Kind != mediadomain.MediaImage && attachment.Kind != mediadomain.MediaSticker {
			continue
		}

		memeID := buildMemeID(attachment)
		err := s.store.UpsertMeme(ctx, mediadomain.MemeAsset{
			MemeID:         memeID,
			GroupID:        event.GroupID,
			SourceEventID:  event.EventID,
			ObjectKey:      attachment.ObjectKey,
			FileExt:        fileExt(attachment.ObjectKey),
			ContentHash:    coalesce(attachment.ContentHash, memeID),
			PerceptualHash: coalesce(attachment.ContentHash, memeID),
			Width:          attachment.Width,
			Height:         attachment.Height,
			Animated:       strings.EqualFold(fileExt(attachment.ObjectKey), ".gif"),
			Status:         "approved",
			CreatedAt:      time.Now(),
		}, mediadomain.MemeDescriptor{
			MemeID:      memeID,
			Title:       "群聊图片",
			Summary:     fmt.Sprintf("来自群聊的%s素材", attachment.Kind),
			Keywords:    []string{string(attachment.Kind), "群聊"},
			EmotionTags: []string{"neutral"},
			SceneTags:   []string{"group"},
			UsageHints:  []string{"回复时可作为图梗素材"},
			Language:    "zh",
			Confidence:  0.4,
			Reviewed:    true,
			UpdatedAt:   time.Now(),
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Search(ctx context.Context, query ports.MemeQuery) ([]mediadomain.MemeSearchResult, error) {
	return s.store.SearchMemes(ctx, query)
}

func (s *Service) BuildSendSegments(ctx context.Context, memeID string, replyToMessageID string, caption string) ([]conversationdomain.MessageSegment, error) {
	asset, _, err := s.store.GetMeme(ctx, memeID)
	if err != nil {
		return nil, err
	}

	segments := make([]conversationdomain.MessageSegment, 0, 3)
	if replyToMessageID != "" {
		segments = append(segments, conversationdomain.MessageSegment{
			Type: "reply",
			Data: map[string]any{"id": replyToMessageID},
		})
	}
	segments = append(segments, conversationdomain.MessageSegment{
		Type: "image",
		Data: map[string]any{"file": asset.ObjectKey},
	})
	if strings.TrimSpace(caption) != "" {
		segments = append(segments, conversationdomain.MessageSegment{
			Type: "text",
			Data: map[string]any{"text": caption},
		})
	}
	return segments, nil
}

func buildMemeID(attachment mediadomain.MultimodalAttachment) string {
	if attachment.ContentHash != "" {
		return "meme-" + attachment.ContentHash
	}
	hash := sha1.Sum([]byte(attachment.ObjectKey + attachment.URL))
	return "meme-" + hex.EncodeToString(hash[:8])
}

func fileExt(path string) string {
	if idx := strings.LastIndex(path, "."); idx >= 0 {
		return path[idx:]
	}
	return ".bin"
}

func coalesce(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
