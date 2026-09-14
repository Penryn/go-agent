package prompting

import (
	"strings"
	"testing"

	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

func TestMemorySnippetsDeduplicateAndKeepHighestRanked(t *testing.T) {
	records := []memorydomain.MemoryRecord{
		{MemoryID: "best", Type: "fact", Content: "最相关"},
		{MemoryID: "best", Type: "fact", Content: "重复副本"},
		{MemoryID: "later", Type: "fact", Content: "次相关"},
	}

	snippets := memorySnippets(records, 10_000)
	if len(snippets) != 2 {
		t.Fatalf("memory records were not deduplicated: %#v", snippets)
	}
	if !strings.Contains(snippets[0], "最相关") || strings.Contains(strings.Join(snippets, "\n"), "重复副本") {
		t.Fatalf("retrieval ranking was not preserved: %#v", snippets)
	}
}

func TestRetainLeadingStringsBoundsOversizedFirstItem(t *testing.T) {
	values := []string{strings.Repeat("记忆", 100), "later"}
	kept, truncated := retainLeadingStrings(values, 31)
	if !truncated || len(kept) != 1 || len(kept[0]) > 31 {
		t.Fatalf("oversized leading item was not bounded: truncated=%v values=%#v", truncated, kept)
	}
}
