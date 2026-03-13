package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	"github.com/phlin/go-agent/internal/core/ports"
)

func TestMarkIntentAndQuery(t *testing.T) {
	store := inmemory.NewStore()
	service := New(store)

	record, err := service.MarkIntent(context.Background(), WriteIntent{
		Scope:      "group:1",
		MemoryType: "preference",
		Subject:    "群话题",
		Content:    "这个群喜欢聊表情包",
		Importance: 0.8,
		Confidence: 0.9,
	})
	if err != nil {
		t.Fatalf("mark intent: %v", err)
	}

	records, err := service.Query(context.Background(), ports.MemoryQuery{
		Scope: "group:1",
		Query: "表情包",
		TopK:  3,
	})
	if err != nil {
		t.Fatalf("query memory: %v", err)
	}
	if len(records) == 0 || records[0].MemoryID != record.MemoryID {
		t.Fatalf("unexpected records: %#v", records)
	}
}

func TestMarkIntentAutoIDContainsType(t *testing.T) {
	store := inmemory.NewStore()
	service := New(store)

	record, err := service.MarkIntent(context.Background(), WriteIntent{
		Scope:      "group:1",
		MemoryType: "group_slang",
		Subject:    "test",
		Content:    "test content",
	})
	if err != nil {
		t.Fatalf("mark intent: %v", err)
	}
	if !strings.HasPrefix(record.MemoryID, "mem-group_slang-") {
		t.Fatalf("auto-generated ID should embed MemoryType, got %q", record.MemoryID)
	}
}

func TestMarkIntentExplicitIDPreserved(t *testing.T) {
	store := inmemory.NewStore()
	service := New(store)

	record, err := service.MarkIntent(context.Background(), WriteIntent{
		MemoryID:   "custom-id-123",
		Scope:      "group:1",
		MemoryType: "preference",
		Subject:    "test",
		Content:    "test content",
	})
	if err != nil {
		t.Fatalf("mark intent: %v", err)
	}
	if record.MemoryID != "custom-id-123" {
		t.Fatalf("explicit MemoryID should be preserved, got %q", record.MemoryID)
	}
}
