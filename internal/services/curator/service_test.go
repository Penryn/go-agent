package curator

import (
	"context"
	"testing"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
)

func TestRun(t *testing.T) {
	service, err := New(context.Background())
	if err != nil {
		t.Fatalf("new curator service: %v", err)
	}

	output, err := service.Run(context.Background(), Input{
		Snapshot: conversationdomain.ContextSnapshot{
			Event: conversationdomain.ConversationEvent{
				EventID: "e1",
				Text:    "这个群今天又在复读离谱梗",
			},
		},
	})
	if err != nil {
		t.Fatalf("run curator: %v", err)
	}
	if len(output.MemoryIntents) == 0 {
		t.Fatalf("expected memory intents")
	}
}
