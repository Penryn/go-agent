package prompting

import (
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
