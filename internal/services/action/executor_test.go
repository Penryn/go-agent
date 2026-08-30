package action

import (
	"context"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	"github.com/phlin/go-agent/internal/config"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	memesvc "github.com/phlin/go-agent/internal/services/meme"
)

func TestExecuteSendMemeAndRecall(t *testing.T) {
	store := inmemory.NewStore()
	sender := inmemory.NewSender()
	memeService := memesvc.New(store, config.MemeConfig{})

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

	executor := New(sender, memeService, nil)
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

func TestExecuteReact(t *testing.T) {
	sender := inmemory.NewSender()
	executor := New(sender, nil, nil)

	_, err := executor.Execute(context.Background(), conversationEvent(), policydomain.AutonomyDecision{
		DecisionID: "d-react",
		Action:     policydomain.ActionReact,
	}, replydomain.ReplyPlan{
		SendMode: "group",
		ActionParams: map[string]any{
			"message_id": "msg-999",
			"emoji_id":   "128077",
		},
	})
	if err != nil {
		t.Fatalf("execute react: %v", err)
	}

	actions := sender.Actions()
	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}
	if actions[0].Kind != policydomain.ActionReact {
		t.Fatalf("expected ActionReact, got %s", actions[0].Kind)
	}
	if actions[0].TargetMessageID != "msg-999" {
		t.Fatalf("expected target message msg-999, got %s", actions[0].TargetMessageID)
	}
	if actions[0].Meta["emoji_id"] != "128077" {
		t.Fatalf("expected emoji_id 128077, got %v", actions[0].Meta["emoji_id"])
	}
}

func TestExecuteReactFallsBackToEventMessageID(t *testing.T) {
	sender := inmemory.NewSender()
	executor := New(sender, nil, nil)

	_, err := executor.Execute(context.Background(), conversationEvent(), policydomain.AutonomyDecision{
		DecisionID: "d-react-fallback",
		Action:     policydomain.ActionReact,
	}, replydomain.ReplyPlan{
		SendMode:     "group",
		ActionParams: map[string]any{"emoji_id": "128516"},
	})
	if err != nil {
		t.Fatalf("execute react fallback: %v", err)
	}

	actions := sender.Actions()
	if len(actions) != 1 || actions[0].TargetMessageID != "m1" {
		t.Fatalf("expected fallback to event message id m1, got %s", actions[0].TargetMessageID)
	}
}

func TestExecuteRhythmSendsBubblesSeparately(t *testing.T) {
	sender := inmemory.NewSender()
	executor := New(sender, nil, nil, WithBubbleDelay(0))
	_, err := executor.Execute(context.Background(), conversationEvent(), policydomain.AutonomyDecision{
		DecisionID: "d-rhythm",
		Action:     policydomain.ActionReply,
	}, replydomain.ReplyPlan{Bubbles: []string{"先说", "后说"}, ReplyToMessageID: "m1", SendMode: "group"})
	if err != nil {
		t.Fatalf("execute rhythm: %v", err)
	}
	actions := sender.Actions()
	if len(actions) != 2 {
		t.Fatalf("expected two bubble actions, got %d: %#v", len(actions), actions)
	}
	if actions[0].ReplyToMessageID != "m1" || actions[1].ReplyToMessageID != "" {
		t.Fatalf("quote should only be attached to first bubble: %#v", actions)
	}
}

func conversationEvent() conversationdomain.ConversationEvent {
	return conversationdomain.ConversationEvent{GroupID: 1, MessageID: "m1"}
}
