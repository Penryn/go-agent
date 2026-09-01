package profile

import (
	"context"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/testsupport"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
)

func TestFamiliarityEvidenceReflectsInteractionQuality(t *testing.T) {
	if got := familiarityEvidence(conversationdomain.ConversationEvent{}); got != 0 {
		t.Fatalf("empty event evidence = %v, want 0", got)
	}
	plain := familiarityEvidence(conversationdomain.ConversationEvent{Text: "嗯"})
	direct := familiarityEvidence(conversationdomain.ConversationEvent{Text: "能帮我看看吗", MentionedBot: true})
	if direct <= plain {
		t.Fatalf("direct evidence = %v, plain = %v", direct, plain)
	}
}

func TestObserveEventUpdatesProfile(t *testing.T) {
	store := testsupport.NewStore(t)
	service := New(store, "")

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

func TestObserveEventInitializesAffinityOnFirstMessage(t *testing.T) {
	store := testsupport.NewStore(t)
	service := New(store, "persona-1")
	ctx := context.Background()
	event := conversationdomain.ConversationEvent{GroupID: 1, UserID: 9, Text: "在吗", TimestampUnix: time.Now().Unix()}

	if err := service.ObserveEvent(ctx, event); err != nil {
		t.Fatalf("first observe: %v", err)
	}
	rel, err := store.GetRelationship(ctx, "persona-1", 1, 9)
	if err != nil {
		t.Fatalf("get relationship: %v", err)
	}
	if rel.Affinity != 0.25 {
		t.Fatalf("first message should seed affinity 0.25, got %v", rel.Affinity)
	}

	// 后续消息不再改写 affinity（只能由 update_affinity 工具调整）
	if err := service.ObserveEvent(ctx, event); err != nil {
		t.Fatalf("second observe: %v", err)
	}
	rel, err = store.GetRelationship(ctx, "persona-1", 1, 9)
	if err != nil {
		t.Fatalf("get relationship again: %v", err)
	}
	if rel.Affinity != 0.25 {
		t.Fatalf("passive observe must not move affinity, got %v", rel.Affinity)
	}
}
