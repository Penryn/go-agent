package inmemory

import (
	"context"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

func TestRecentEventsUsesChronologicalCursorOrder(t *testing.T) {
	store := NewStore()
	ctx := context.Background()
	for _, event := range []conversationdomain.ConversationEvent{
		{EventID: "late", GroupID: 1, TimestampUnix: 30},
		{EventID: "same-b", GroupID: 1, TimestampUnix: 20},
		{EventID: "early", GroupID: 1, TimestampUnix: 10},
		{EventID: "same-a", GroupID: 1, TimestampUnix: 20},
	} {
		if err := store.ArchiveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	events, err := store.EventsAfter(ctx, 1, time.Unix(10, 0), "early", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].EventID != "same-a" || events[1].EventID != "same-b" || events[2].EventID != "late" {
		t.Fatalf("unexpected cursor order: %+v", events)
	}
}

func TestQueryMemoriesDecaysOldLowImportance(t *testing.T) {
	store := NewStore()
	ctx := context.Background()
	now := time.Now()

	fresh := memorydomain.MemoryRecord{MemoryID: "fresh", Scope: "group:1", Type: "preference", Subject: "口味", Content: "爱吃甜的", Importance: 0.3, CreatedAt: now.Add(-time.Hour)}
	old := memorydomain.MemoryRecord{MemoryID: "old", Scope: "group:1", Type: "preference", Subject: "旧事", Content: "上个月聊过露营", Importance: 0.3, CreatedAt: now.Add(-30 * 24 * time.Hour)}
	important := memorydomain.MemoryRecord{MemoryID: "important", Scope: "group:1", Type: "preference", Subject: "重要", Content: "下月生日", Importance: 0.9, CreatedAt: now.Add(-30 * 24 * time.Hour)}
	expired := memorydomain.MemoryRecord{MemoryID: "expired", Scope: "group:1", Type: "topic_keyword", Subject: "过期", Content: "已失效的热点", Importance: 0.9, CreatedAt: now.Add(-48 * time.Hour), ExpiresAt: ptrTime(now.Add(-time.Hour))}

	for _, record := range []memorydomain.MemoryRecord{fresh, old, important, expired} {
		if err := store.UpsertMemory(ctx, record); err != nil {
			t.Fatalf("upsert %s: %v", record.MemoryID, err)
		}
	}

	records, err := store.QueryMemories(ctx, ports.MemoryQuery{GroupID: 1, TopK: 10})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// 过期的不召回
	for _, record := range records {
		if record.MemoryID == "expired" {
			t.Fatal("expired memory must not be recalled")
		}
	}
	// 高分旧记忆 > 低分旧记忆（旧不重要的事该淡出）；同分时新的在前
	if len(records) < 3 || records[0].MemoryID != "important" {
		t.Fatalf("expected important memory ranked first, got %+v", records)
	}
	var oldRank, freshRank = -1, -1
	for i, record := range records {
		switch record.MemoryID {
		case "old":
			oldRank = i
		case "fresh":
			freshRank = i
		}
	}
	if oldRank != -1 && freshRank != -1 && oldRank < freshRank {
		t.Fatalf("fresh memory should outrank equally-important old one: %+v", records)
	}
}

func TestQueryMemoriesRespectsGroupAndUserVisibility(t *testing.T) {
	store := NewStore()
	ctx := context.Background()
	for _, record := range []memorydomain.MemoryRecord{
		{MemoryID: "global", Scope: "global", Subject: "shared", Content: "shared"},
		{MemoryID: "group-1", Scope: "group:1", Subject: "group", Content: "group"},
		{MemoryID: "user-7", Scope: "group:1:user:7", Subject: "user", Content: "user"},
		{MemoryID: "group-2", Scope: "group:2", Subject: "other", Content: "other"},
	} {
		if err := store.UpsertMemory(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	records, err := store.QueryMemories(ctx, ports.MemoryQuery{GroupID: 1, UserID: 7, TopK: 10})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, record := range records {
		seen[record.MemoryID] = true
	}
	if !seen["global"] || !seen["group-1"] || !seen["user-7"] || seen["group-2"] {
		t.Fatalf("unexpected visible memories: %+v", records)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
