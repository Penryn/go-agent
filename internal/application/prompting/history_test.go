package prompting

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
)

func TestMessagesRebuildDialogueFromArchivedEvents(t *testing.T) {
	composer := NewComposer(defaultPersona())
	decision := policydomain.AutonomyDecision{TriggerType: "answer"}
	snapshot := conversationdomain.ContextSnapshot{
		SelfID: 99,
		Event: conversationdomain.ConversationEvent{
			EventID: "in-2", UserID: 2, Sender: conversationdomain.SenderIdentity{GroupCard: "小林"}, Text: "那后来呢",
		},
		RecentTurns: []conversationdomain.ConversationEvent{
			{EventID: "in-1", UserID: 2, Sender: conversationdomain.SenderIdentity{GroupCard: "小林"}, Text: "先说第一句"},
			{EventID: "out-1", UserID: 99, Sender: conversationdomain.SenderIdentity{GroupCard: "小菲"}, Text: "我听着呢", Origin: "outbound"},
			{EventID: "in-2", UserID: 2, Sender: conversationdomain.SenderIdentity{GroupCard: "小林"}, Text: "那后来呢"},
		},
	}

	messages := composer.Messages(snapshot, decision)
	if len(messages) != 4 {
		t.Fatalf("unexpected rebuilt message count: %d", len(messages))
	}
	if messages[0].Role != schema.User || messages[1].Role != schema.Assistant {
		t.Fatalf("archived roles were not preserved: %s %s", messages[0].Role, messages[1].Role)
	}
	if messages[2].Role != schema.User || messages[2].Content == "" {
		t.Fatalf("current event missing from rebuilt turn: %#v", messages[2])
	}
	for _, message := range messages {
		if message.Role == schema.Tool || len(message.ToolCalls) > 0 {
			t.Fatalf("model scratch leaked into rebuilt dialogue: %#v", message)
		}
	}
}

func TestPrepareDialogueTurnsMergesRapidSplitMessage(t *testing.T) {
	current := conversationdomain.ConversationEvent{EventID: "e3", UserID: 2, Text: "说完", TimestampUnix: 102}
	history, merged := prepareDialogueTurns([]conversationdomain.ConversationEvent{
		{EventID: "e1", UserID: 2, Text: "我还没", TimestampUnix: 100},
		{EventID: "e2", UserID: 2, Text: "说", TimestampUnix: 101},
	}, current, 99)

	if len(history) != 0 || merged.Text != "我还没 说 说完" {
		t.Fatalf("split message was not merged: history=%#v current=%q", history, merged.Text)
	}
}

func TestPrepareDialogueTurnsPinsReplyTargetAndCurrentSpeaker(t *testing.T) {
	turns := make([]conversationdomain.ConversationEvent, 0, 12)
	turns = append(turns,
		conversationdomain.ConversationEvent{EventID: "reply-target", MessageID: "m-target", UserID: 7, Text: "被引用的旧消息"},
		conversationdomain.ConversationEvent{EventID: "speaker-old", UserID: 2, Text: "当前说话人的旧消息"},
	)
	for i := 0; i < 10; i++ {
		turns = append(turns, conversationdomain.ConversationEvent{EventID: "tail-" + string(rune('a'+i)), UserID: int64(10 + i), Text: "尾部消息"})
	}
	current := conversationdomain.ConversationEvent{EventID: "current", UserID: 2, ReplyToMessageID: "m-target", Text: "接着说"}

	history, _ := prepareDialogueTurns(turns, current, 99)
	var joined strings.Builder
	for _, turn := range history {
		joined.WriteString(turn.Text)
		joined.WriteByte('\n')
	}
	if !strings.Contains(joined.String(), "被引用的旧消息") || !strings.Contains(joined.String(), "当前说话人的旧消息") {
		t.Fatalf("relevant older turns were not pinned: %s", joined.String())
	}
	if len(history) != 10 {
		t.Fatalf("unexpected focused history size: %d", len(history))
	}
}

func TestMessagesDoNotInventUndeliveredAssistantTurns(t *testing.T) {
	composer := NewComposer(defaultPersona())
	snapshot := conversationdomain.ContextSnapshot{
		SelfID: 99,
		Event:  conversationdomain.ConversationEvent{EventID: "in-2", UserID: 2, Text: "还在吗"},
		RecentTurns: []conversationdomain.ConversationEvent{
			{EventID: "in-1", UserID: 2, Text: "上一句"},
		},
	}

	for _, message := range composer.Messages(snapshot, policydomain.AutonomyDecision{}) {
		if message.Role == schema.Assistant {
			t.Fatalf("assistant turn appeared without an archived outbound event: %#v", message)
		}
	}
}
