package curator

import (
	"testing"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
)

func TestExtract(t *testing.T) {
	intents := Extract(conversationdomain.ContextSnapshot{
		Event: conversationdomain.ConversationEvent{
			EventID: "e1",
			Text:    "这个群今天又在复读离谱梗",
		},
	})
	if len(intents) == 0 {
		t.Fatalf("expected memory intents")
	}
}
