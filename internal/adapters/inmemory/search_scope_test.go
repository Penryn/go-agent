package inmemory

import (
	"context"
	"testing"

	"github.com/phlin/go-agent/internal/core/ports"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

func TestQueryMemoriesScopeCannotBypassSession(t *testing.T) {
	store := NewStore()
	ctx := context.Background()
	for _, record := range []memorydomain.MemoryRecord{
		{MemoryID: "global", Scope: "global", Content: "global fact"},
		{MemoryID: "group-1", Scope: "group:1", Content: "group one fact"},
		{MemoryID: "user-7", Scope: "group:1:user:7", Content: "user fact"},
		{MemoryID: "group-2", Scope: "group:2", Content: "foreign fact"},
	} {
		if err := store.UpsertMemory(ctx, record); err != nil {
			t.Fatal(err)
		}
	}

	queries := []struct {
		name  string
		query ports.MemoryQuery
		want  map[string]bool
	}{
		{name: "global", query: ports.MemoryQuery{GroupID: 0, Scope: "global"}, want: map[string]bool{"global": true}},
		{name: "group", query: ports.MemoryQuery{GroupID: 1, Scope: "group:1"}, want: map[string]bool{"group-1": true}},
		{name: "user", query: ports.MemoryQuery{GroupID: 1, UserID: 7, Scope: "group:1:user:7"}, want: map[string]bool{"user-7": true}},
		{name: "foreign group", query: ports.MemoryQuery{GroupID: 1, Scope: "group:2"}, want: map[string]bool{}},
		{name: "group scope without session", query: ports.MemoryQuery{GroupID: 0, Scope: "group:1"}, want: map[string]bool{}},
	}
	for _, tt := range queries {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.QueryMemories(ctx, tt.query)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d records, want %d: %+v", len(got), len(tt.want), got)
			}
			for _, record := range got {
				if !tt.want[record.MemoryID] {
					t.Fatalf("unexpected record: %+v", record)
				}
			}
		})
	}
}
