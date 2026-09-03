package prompting

import (
	"strings"
	"testing"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
)

func TestSessionMessagesAppendWithoutRebuildingHistory(t *testing.T) {
	composer := NewComposer(defaultPersona())
	decision := policydomain.AutonomyDecision{TriggerType: "answer"}
	firstSnapshot := conversationdomain.ContextSnapshot{
		Event: conversationdomain.ConversationEvent{EventID: "e1", UserID: 2, Text: "第一句"},
	}
	first, session := composer.sessionMessages(firstSnapshot, decision, "tools-v1")
	if len(first) != 2 || len(session.Messages) != 2 {
		t.Fatalf("unexpected first session: messages=%d session=%d", len(first), len(session.Messages))
	}

	secondSnapshot := firstSnapshot
	secondSnapshot.Event = conversationdomain.ConversationEvent{EventID: "e2", UserID: 3, Text: "第二句"}
	secondSnapshot.PromptSession = session
	second, _ := composer.sessionMessages(secondSnapshot, decision, "tools-v1")
	if len(second) != 4 {
		t.Fatalf("session did not append current turn: %d", len(second))
	}
	if second[0].Content != first[0].Content || second[1].Content != first[1].Content {
		t.Fatal("previous prompt messages were rebuilt")
	}
}

func TestSessionVersionChangesWithToolSchema(t *testing.T) {
	composer := NewComposer(defaultPersona())
	decision := policydomain.AutonomyDecision{TriggerType: "answer"}
	snapshot := conversationdomain.ContextSnapshot{Event: conversationdomain.ConversationEvent{Text: "第一句"}}
	_, session := composer.sessionMessages(snapshot, decision, "tools-v1")
	snapshot.PromptSession = session
	_, reset := composer.sessionMessages(snapshot, decision, "tools-v2")
	if len(reset.Messages) != 2 || reset.Version == session.Version {
		t.Fatalf("tool schema change did not reset session: old=%q new=%q messages=%d", session.Version, reset.Version, len(reset.Messages))
	}
}

func TestCompactPromptMessagesIsBoundedAndDeterministic(t *testing.T) {
	messages := []conversationdomain.PromptMessage{
		{Role: "user", Content: strings.Repeat("旧消息 ", 1200)},
		{Role: "assistant", Content: strings.Repeat("新的消息 ", 1200)},
	}
	first := compactPromptMessages(messages)
	second := compactPromptMessages(messages)
	if len(first) != 1 || first[0].Content != second[0].Content {
		t.Fatalf("checkpoint is not deterministic: %#v %#v", first, second)
	}
	if promptSessionBytes(first) >= promptSessionBytes(messages) {
		t.Fatal("checkpoint did not reduce the prompt")
	}
}
