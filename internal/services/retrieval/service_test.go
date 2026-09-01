package retrieval

import (
	"context"
	"errors"
	"testing"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	"github.com/phlin/go-agent/internal/core/ports"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

type vectorMemoryFake struct {
	records []memorydomain.MemoryRecord
	err     error
}

func (f vectorMemoryFake) StoreMemory(context.Context, memorydomain.MemoryRecord) error { return nil }
func (f vectorMemoryFake) SearchMemories(context.Context, string, int64, int64, int, float64) ([]memorydomain.MemoryRecord, error) {
	return f.records, f.err
}

type vectorMemeFake struct {
	results []mediadomain.MemeSearchResult
	err     error
}

func (f vectorMemeFake) IndexMeme(context.Context, string, string, int64) error { return nil }
func (f vectorMemeFake) SearchMemes(context.Context, int64, string, int, float64) ([]mediadomain.MemeSearchResult, error) {
	return f.results, f.err
}
func (f vectorMemeFake) DeleteMeme(context.Context, string) error { return nil }

type lexicalMemoryFake struct {
	records []memorydomain.MemoryRecord
	calls   int
}

func (f *lexicalMemoryFake) SearchMemoriesLexical(context.Context, ports.MemoryQuery) ([]memorydomain.MemoryRecord, error) {
	f.calls++
	return f.records, nil
}

func TestSearchUsesExplicitLexicalAdapter(t *testing.T) {
	store := inmemory.NewStore()
	lexical := &lexicalMemoryFake{records: []memorydomain.MemoryRecord{{MemoryID: "from-index", Scope: "group:1", Content: "indexed"}}}
	retriever := New(store, store, nil, nil, Config{}, WithLexicalAdapters(lexical, nil))
	results, err := retriever.SearchMemories(context.Background(), ports.MemoryQuery{GroupID: 1, Query: "query", TopK: 1})
	if err != nil {
		t.Fatal(err)
	}
	if lexical.calls != 1 || len(results) != 1 || results[0].MemoryID != "from-index" {
		t.Fatalf("lexical adapter was not used: calls=%d results=%+v", lexical.calls, results)
	}
}

func TestSearchMemoriesUsesBM25AndVectorWithRRF(t *testing.T) {
	store := inmemory.NewStore()
	ctx := context.Background()
	for _, record := range []memorydomain.MemoryRecord{
		{MemoryID: "lexical", Scope: "group:1", Subject: "精确", Content: "旧梗"},
		{MemoryID: "semantic", Scope: "group:1", Subject: "语义", Content: "只在语义轨命中"},
	} {
		if err := store.UpsertMemory(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	retriever := New(store, store, vectorMemoryFake{records: []memorydomain.MemoryRecord{{MemoryID: "semantic", Scope: "group:1", Content: "只在语义轨命中"}}}, nil, Config{MemoryCandidateK: 2})
	results, err := retriever.SearchMemories(ctx, ports.MemoryQuery{GroupID: 1, Query: "旧梗", TopK: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].MemoryID != "lexical" || results[1].MemoryID != "semantic" {
		t.Fatalf("unexpected hybrid results: %+v", results)
	}
}

func TestSearchMemoriesFailsClosedForForeignScope(t *testing.T) {
	store := inmemory.NewStore()
	retriever := New(store, store, nil, nil, Config{})
	_, err := retriever.SearchMemories(context.Background(), ports.MemoryQuery{GroupID: 1, UserID: 7, Scope: "group:2", Query: "x"})
	if !errors.Is(err, ErrScopeForbidden) {
		t.Fatalf("expected scope error, got %v", err)
	}
}

func TestSearchMemoriesDegradesWhenVectorFails(t *testing.T) {
	store := inmemory.NewStore()
	if err := store.UpsertMemory(context.Background(), memorydomain.MemoryRecord{MemoryID: "lexical", Scope: "group:1", Content: "旧梗"}); err != nil {
		t.Fatal(err)
	}
	retriever := New(store, store, vectorMemoryFake{err: errors.New("embedding unavailable")}, nil, Config{})
	results, err := retriever.SearchMemories(context.Background(), ports.MemoryQuery{GroupID: 1, Query: "旧梗", TopK: 1})
	if err != nil || len(results) != 1 || results[0].MemoryID != "lexical" {
		t.Fatalf("unexpected lexical fallback: results=%+v err=%v", results, err)
	}
}
