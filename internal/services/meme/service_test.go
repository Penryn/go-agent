package meme

import (
	"context"
	"testing"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
)

func TestObserveEventAndBuildSendSegments(t *testing.T) {
	store := inmemory.NewStore()
	service := New(store, config.MemeConfig{AutoCollect: true})

	err := service.ObserveEvent(context.Background(), conversationdomain.ConversationEvent{
		EventID: "e1",
		GroupID: 1,
		Attachments: []mediadomain.MultimodalAttachment{
			{
				AttachmentID: "a1",
				Kind:         mediadomain.MediaSticker,
				ObjectKey:    "memes/a1.webp",
				ContentHash:  "hash-a1",
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("observe event: %v", err)
	}

	results, err := service.Search(context.Background(), ports.MemeQuery{GroupID: 1, Query: "群聊", TopK: 3})
	if err != nil {
		t.Fatalf("search meme: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected search results")
	}

	segments, err := service.BuildSendSegments(context.Background(), "meme-hash-a1", "reply-1", "收下这张")
	if err != nil {
		t.Fatalf("build send segments: %v", err)
	}
	if len(segments) < 2 || segments[0].Type != "reply" || segments[1].Type != "image" {
		t.Fatalf("unexpected segments: %#v", segments)
	}
}
