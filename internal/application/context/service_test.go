package context

import (
	"testing"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
)

func TestMergeRecentTurnsOrdersAndBoundsTheProjection(t *testing.T) {
	merged := mergeRecentTurns(
		[]conversationdomain.ConversationEvent{
			{EventID: "b", TimestampUnix: 20},
			{EventID: "d", TimestampUnix: 40},
		},
		[]conversationdomain.ConversationEvent{
			{EventID: "a", TimestampUnix: 10},
			{EventID: "b", TimestampUnix: 20},
			{EventID: "c", TimestampUnix: 30},
		},
		3,
	)
	if len(merged) != 3 || merged[0].EventID != "b" || merged[1].EventID != "c" || merged[2].EventID != "d" {
		t.Fatalf("unexpected recent turns: %+v", merged)
	}
}
