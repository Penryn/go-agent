package profile

import (
	"context"
	"testing"
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	"github.com/phlin/go-agent/internal/testsupport"
)

func TestObserveEventUpdatesProfile(t *testing.T) {
	store := testsupport.NewStore(t)
	service := New(store, 99)

	err := service.ObserveEvent(context.Background(), conversationdomain.ConversationEvent{
		GroupID:       1,
		UserID:        2,
		Text:          "离谱",
		TimestampUnix: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("observe event: %v", err)
	}

	profile, err := store.GetMemberProfile(context.Background(), 1, 2)
	if err != nil {
		t.Fatalf("query profile: %v", err)
	}
	if profile.Stats.MessageCount != 1 {
		t.Fatalf("unexpected message count: %d", profile.Stats.MessageCount)
	}
	if len(profile.CommonPhrases) == 0 {
		t.Fatalf("expected common phrase")
	}
}

func TestObserveEventSkipsOutboundAndSelfMessages(t *testing.T) {
	store := testsupport.NewStore(t)
	service := New(store, 99)

	for _, event := range []conversationdomain.ConversationEvent{
		{GroupID: 1, UserID: 99, Origin: "outbound", Text: "机器人自己的话", TimestampUnix: time.Now().Unix()},
		{GroupID: 1, UserID: 99, Text: "即使来源缺失也不能写入", TimestampUnix: time.Now().Unix()},
	} {
		if err := service.ObserveEvent(context.Background(), event); err != nil {
			t.Fatalf("observe event: %v", err)
		}
	}

	profile, err := store.GetMemberProfile(context.Background(), 1, 99)
	if err != nil {
		t.Fatalf("query profile: %v", err)
	}
	if profile.Stats.MessageCount != 0 {
		t.Fatalf("expected outbound/self events to be ignored, got %d", profile.Stats.MessageCount)
	}
}
