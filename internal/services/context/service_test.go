package context

import (
	"testing"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

func TestMergeMemoryResultsUsesRRFAndKeepsAuthoritativeFields(t *testing.T) {
	structured := []memorydomain.MemoryRecord{
		{MemoryID: "exact", Subject: "偏好", Content: "完整内容", Importance: 0.2},
		{MemoryID: "lexical", Subject: "词", Content: "关键词命中"},
	}
	semantic := []memorydomain.MemoryRecord{
		{MemoryID: "exact", Subject: "偏好", Content: "", Importance: 0.9},
		{MemoryID: "semantic", Subject: "语义", Content: "只在语义轨命中"},
	}

	merged := mergeMemoryResults(structured, semantic, 3)
	if len(merged) != 3 {
		t.Fatalf("expected three unique memories, got %d: %+v", len(merged), merged)
	}
	if merged[0].MemoryID != "exact" {
		t.Fatalf("expected fused exact result first, got %+v", merged)
	}
	if merged[0].Content != "完整内容" {
		t.Fatalf("lexical record should provide complete content, got %q", merged[0].Content)
	}
	seen := map[string]bool{}
	for _, record := range merged {
		if seen[record.MemoryID] {
			t.Fatalf("duplicate memory in fused result: %+v", merged)
		}
		seen[record.MemoryID] = true
	}
}

func TestMergeMemoryResultsRespectsLimit(t *testing.T) {
	merged := mergeMemoryResults(
		[]memorydomain.MemoryRecord{{MemoryID: "a"}, {MemoryID: "b"}},
		[]memorydomain.MemoryRecord{{MemoryID: "c"}, {MemoryID: "d"}},
		2,
	)
	if len(merged) != 2 {
		t.Fatalf("expected limit to be applied, got %d", len(merged))
	}
}

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
