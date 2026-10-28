package chat

import (
	"testing"

	"github.com/phlin/go-agent/internal/qq"
)

func TestBuildConversationContextRelations(t *testing.T) {
	history := []qq.Event{
		{ID: "m1", MessageID: "1", UserID: 10, Text: "直接问你", Directed: true},
		{ID: "out", MessageID: "2", UserID: 99, Text: "我的回答", Outbound: true},
		{ID: "old", MessageID: "6", UserID: 12, Text: "旧闲聊"},
		{ID: "m5", MessageID: "5", UserID: 13, Text: "撤回", Recalled: true},
	}
	batch := []qq.Event{
		{ID: "m3", MessageID: "3", UserID: 11, Text: "回复机器人", ReplyTo: "2"},
		{ID: "m4", MessageID: "4", UserID: 11, Text: "接着说"},
		{ID: "m6", MessageID: "6", UserID: 12, Text: "另一个人闲聊"},
	}
	context := buildConversationContext(batch, history, map[int64]bool{11: true})

	want := map[string]string{"m3": "reply_to_bot", "m4": "continuation", "m6": "ordinary"}
	for _, event := range context.Batch {
		if event.Relation != want[event.EventID] {
			t.Fatalf("batch event %s relation = %q, want %q", event.EventID, event.Relation, want[event.EventID])
		}
	}
	want = map[string]string{"m1": "direct", "out": "bot_reply", "old": "ordinary", "m5": "recalled"}
	for _, event := range context.History {
		if event.Relation != want[event.EventID] {
			t.Fatalf("history event %s relation = %q, want %q", event.EventID, event.Relation, want[event.EventID])
		}
	}
}

func TestReplyParticipantsFollowsTarget(t *testing.T) {
	events := []qq.Event{
		{ID: "direct", UserID: 10, Directed: true},
		{ID: "ordinary", UserID: 11},
	}
	participants := replyParticipants(events, &ReplyPlan{TargetEventID: "ordinary"})
	if len(participants) != 1 || !participants[11] {
		t.Fatalf("participants = %v, want only target user", participants)
	}

	participants = replyParticipants(events, &ReplyPlan{})
	if len(participants) != 1 || !participants[10] {
		t.Fatalf("fallback participants = %v, want direct user", participants)
	}
}
