package action

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	memesvc "github.com/phlin/go-agent/internal/application/meme"
	"github.com/phlin/go-agent/internal/config"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
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

func TestExecuteRepairRecallsThenReplies(t *testing.T) {
	sender := inmemory.NewSender()
	executor := New(sender, nil, nil)
	receipt, err := executor.Execute(context.Background(), conversationEvent(), policydomain.AutonomyDecision{
		DecisionID: "d-repair",
		Action:     policydomain.ActionRepair,
	}, replydomain.ReplyPlan{
		SendMode: "group",
		ActionParams: map[string]any{
			"message_id":          "bot-old",
			"corrected_text":      "改成这句",
			"reply_to_message_id": "user-msg",
		},
	})
	if err != nil {
		t.Fatalf("execute repair: %v", err)
	}
	actions := sender.Actions()
	if len(actions) != 2 {
		t.Fatalf("expected recall and reply, got %#v", actions)
	}
	if actions[0].Kind != policydomain.ActionRecall || actions[0].TargetMessageID != "bot-old" {
		t.Fatalf("unexpected recall step: %+v", actions[0])
	}
	if actions[1].Kind != policydomain.ActionReply || actions[1].ReplyToMessageID != "user-msg" {
		t.Fatalf("unexpected reply step: %+v", actions[1])
	}
	if len(receipt.StepReceipts) != 2 || receipt.Partial {
		t.Fatalf("unexpected aggregate receipt: %+v", receipt)
	}
}

type failSecondSender struct {
	calls int
}

func (s *failSecondSender) Send(_ context.Context, action replydomain.ActionExecution) (replydomain.ActionReceipt, error) {
	s.calls++
	if s.calls == 2 {
		return replydomain.ActionReceipt{}, errors.New("reply unavailable")
	}
	return replydomain.ActionReceipt{ActionID: action.ActionID, PlatformMessageID: action.TargetMessageID, Sent: true}, nil
}

func TestExecuteRepairReportsPartialSuccess(t *testing.T) {
	sender := &failSecondSender{}
	executor := New(sender, nil, nil)
	receipt, err := executor.Execute(context.Background(), conversationEvent(), policydomain.AutonomyDecision{
		DecisionID: "d-partial",
		Action:     policydomain.ActionRepair,
	}, replydomain.ReplyPlan{
		ActionParams: map[string]any{"message_id": "bot-old", "corrected_text": "纠正"},
	})
	if err == nil {
		t.Fatal("expected replacement send to fail")
	}
	if !receipt.Partial || receipt.DropReason != "repair_reply_failed" || len(receipt.StepReceipts) != 2 {
		t.Fatalf("unexpected partial receipt: %+v", receipt)
	}
	if receipt.StepReceipts[0].ActionID != "d-partial-recall" || receipt.StepReceipts[1].ActionID != "d-partial-reply" {
		t.Fatalf("unexpected repair step receipts: %+v", receipt.StepReceipts)
	}
}

func TestExecuteRhythmSendsBubblesSeparately(t *testing.T) {
	sender := inmemory.NewSender()
	bubbleDelay = 0
	executor := New(sender, nil, nil)
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

func TestExecuteRhythmDropsQueuedBubblesAfterCancellation(t *testing.T) {
	sender := inmemory.NewSender()
	bubbleDelay = 100 * time.Millisecond
	executor := New(sender, nil, nil)
	finished := make(chan error, 1)
	go func() {
		_, err := executor.Execute(context.Background(), conversationEvent(), policydomain.AutonomyDecision{
			DecisionID: "d-cancel",
			Action:     policydomain.ActionReply,
		}, replydomain.ReplyPlan{Bubbles: []string{"先说", "不再发送"}})
		finished <- err
	}()

	deadline := time.Now().Add(time.Second)
	for len(sender.Actions()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := len(sender.Actions()); got != 1 {
		t.Fatalf("expected first bubble before cancellation, got %d", got)
	}

	executor.CancelQueued(conversationEvent().GroupID)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("cancelled rhythm returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled rhythm did not finish")
	}
	if got := len(sender.Actions()); got != 1 {
		t.Fatalf("expected queued bubble to be dropped, got %d actions", got)
	}
}

func TestExecuteSingleBubbleRemainsOneAction(t *testing.T) {
	sender := inmemory.NewSender()
	bubbleDelay = time.Second
	executor := New(sender, nil, nil)

	if _, err := executor.Execute(context.Background(), conversationEvent(), policydomain.AutonomyDecision{
		DecisionID: "d-single",
		Action:     policydomain.ActionReply,
	}, replydomain.ReplyPlan{Bubbles: []string{"单句"}}); err != nil {
		t.Fatalf("execute single bubble: %v", err)
	}
	if got := len(sender.Actions()); got != 1 {
		t.Fatalf("expected one action, got %d", got)
	}
}

func conversationEvent() conversationdomain.ConversationEvent {
	return conversationdomain.ConversationEvent{GroupID: 1, MessageID: "m1"}
}

// readTrackingSender 断言 MarkRead 在 Send 之前被调用。
type readTrackingSender struct {
	inmemory.Sender
	events []string
}

func (s *readTrackingSender) Send(ctx context.Context, action replydomain.ActionExecution) (replydomain.ActionReceipt, error) {
	s.events = append(s.events, "send")
	return s.Sender.Send(ctx, action)
}

func (s *readTrackingSender) MarkRead(ctx context.Context, groupID int64, messageID string) error {
	s.events = append(s.events, "read")
	return s.Sender.MarkRead(ctx, groupID, messageID)
}

func TestExecuteMarksReadBeforeSending(t *testing.T) {
	sender := &readTrackingSender{Sender: *inmemory.NewSender()}
	bubbleDelay = 0
	executor := New(sender, nil, nil)
	event := conversationdomain.ConversationEvent{GroupID: 1, UserID: 2, MessageID: "m-1"}
	plan := replydomain.ReplyPlan{Bubbles: []string{"在"}, SendMode: "single"}
	receipt, err := executor.Execute(context.Background(), event, policydomain.AutonomyDecision{Action: policydomain.ActionReply}, plan)
	if err != nil || !receipt.Sent {
		t.Fatalf("execute: receipt=%+v err=%v", receipt, err)
	}
	if len(sender.events) != 2 || sender.events[0] != "read" || sender.events[1] != "send" {
		t.Fatalf("expected read before send, got %v", sender.events)
	}

	// silent 不应触发已读
	sender.events = nil
	_, err = executor.Execute(context.Background(), event, policydomain.AutonomyDecision{Action: policydomain.ActionSilent}, plan)
	if err != nil {
		t.Fatalf("execute silent: %v", err)
	}
	if len(sender.events) != 0 {
		t.Fatalf("silent decision must not mark read, got %v", sender.events)
	}
}
