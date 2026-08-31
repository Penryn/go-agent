package meme

import (
	"context"
	"fmt"
	"testing"
	"time"

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

func TestDudFeedbackSinksColdMemes(t *testing.T) {
	store := inmemory.NewStore()
	ctx := context.Background()
	svc := New(store, config.MemeConfig{AutoCollect: true, RepeatCooldown: "10m"})

	// 收两张表情
	for i, text := range []string{"好图", "烂图"} {
		event := conversationdomain.ConversationEvent{
			EventID: fmt.Sprintf("ev-%d", i), GroupID: 1, UserID: 7, Text: text,
			Attachments: []mediadomain.MultimodalAttachment{{AttachmentID: fmt.Sprintf("a-%d", i), Kind: mediadomain.MediaImage, ObjectKey: fmt.Sprintf("k%d.jpg", i)}},
		}
		if err := svc.ObserveEvent(ctx, event, nil); err != nil {
			t.Fatalf("observe: %v", err)
		}
	}

	// 两张各发三次
	all := []string{}
	for id := range memeIDs(t, store) {
		all = append(all, id)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 memes, got %v", all)
	}
	for _, id := range all {
		for range 3 {
			if err := store.MarkMemeSent(ctx, id); err != nil {
				t.Fatalf("mark sent: %v", err)
			}
		}
	}

	// 第一张反复哑弹：发送 -> 冷场超窗 -> 检索时兜底结算
	dud := all[0]
	svc.markSentAt(dud)
	svc.sweepExpiredDuds(ctx, time.Now().Add(dudObservationWindow+time.Minute))
	asset, _, err := store.GetMeme(ctx, dud)
	if err != nil {
		t.Fatalf("get dud meme: %v", err)
	}
	if asset.DudCount != 1 || asset.SendCount != 3 {
		t.Fatalf("expected dud=1 send=3, got dud=%d send=%d", asset.DudCount, asset.SendCount)
	}

	// 哑弹率高的应排在后面（空 Query 匹配全部）
	results, err := svc.Search(ctx, ports.MemeQuery{GroupID: 1, Query: "", TopK: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %+v", results)
	}
	if results[len(results)-1].MemeID != dud {
		t.Fatalf("dud meme %s should rank last, got order %+v", dud, results)
	}
}

// memeIds 辅助：inmemory 没有列表接口，从检索拿全部。
func memeIDs(t *testing.T, store *inmemory.Store) map[string]struct{} {
	t.Helper()
	ids := map[string]struct{}{}
	results, err := store.SearchMemes(context.Background(), ports.MemeQuery{GroupID: 1, Query: "", TopK: 10})
	if err != nil {
		t.Fatalf("search all: %v", err)
	}
	for _, r := range results {
		ids[r.MemeID] = struct{}{}
	}
	return ids
}
