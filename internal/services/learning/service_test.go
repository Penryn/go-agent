package learning

import (
	"context"
	"testing"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
)

func TestRun(t *testing.T) {
	service, err := New(context.Background())
	if err != nil {
		t.Fatalf("new learning service: %v", err)
	}

	output, err := service.Run(context.Background(), Input{
		GroupID: 1,
		Events: []conversationdomain.ConversationEvent{
			{Text: "离谱"},
			{Text: "离谱"},
			{Text: "今天正常"},
		},
	})
	if err != nil {
		t.Fatalf("run learning service: %v", err)
	}
	if len(output.Candidates) == 0 || output.Candidates[0].Value != "离谱" {
		t.Fatalf("unexpected candidates: %#v", output.Candidates)
	}
}
