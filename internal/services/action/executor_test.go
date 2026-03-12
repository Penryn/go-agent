package action

import (
	"context"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	memesvc "github.com/phlin/go-agent/internal/services/meme"
)

func TestExecuteSendMemeAndRecall(t *testing.T) {
	store := inmemory.NewStore()
	sender := inmemory.NewSender()
	memeService := memesvc.New(store)

	if err := store.UpsertMeme(context.Background(), mediadomain.MemeAsset{
		MemeID:      "meme-1",
		GroupID:     1,
		ObjectKey:   "memes/a1.webp",
		ContentHash: "hash-a1",
		Status:      "approved",
		CreatedAt:   time.Now(),
	}, mediadomain.MemeDescriptor{
		MemeID:    "meme-1",
		Summary:   "测试图",
		Keywords:  []string{"测试"},
		Reviewed:  true,
		UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed meme: %v", err)
	}

	executor := New(sender, memeService)
	_, err := executor.Execute(context.Background(), conversationEvent(), policydomain.AutonomyDecision{
		DecisionID: "d1",
		Action:     policydomain.ActionMemeOnly,
	}, replydomain.ReplyPlan{
		SendMode: "group",
		ActionParams: map[string]any{
			"meme_id":             "meme-1",
			"reply_to_message_id": "r1",
			"caption":             "收下",
		},
	})
	if err != nil {
		t.Fatalf("execute send meme: %v", err)
	}

	actions := sender.Actions()
	if len(actions) == 0 || len(actions[0].Segments) < 2 || actions[0].Segments[1].Type != "image" {
		t.Fatalf("unexpected meme action: %#v", actions)
	}

	_, err = executor.Execute(context.Background(), conversationEvent(), policydomain.AutonomyDecision{
		DecisionID: "d2",
		Action:     policydomain.ActionRecall,
	}, replydomain.ReplyPlan{
		SendMode: "group",
		ActionParams: map[string]any{
			"message_id": "12345",
		},
	})
	if err != nil {
		t.Fatalf("execute recall: %v", err)
	}
	if len(sender.Actions()) < 2 || sender.Actions()[1].TargetMessageID != "12345" {
		t.Fatalf("unexpected recall action: %#v", sender.Actions())
	}
}

func conversationEvent() conversationdomain.ConversationEvent {
	return conversationdomain.ConversationEvent{GroupID: 1, MessageID: "m1"}
}
