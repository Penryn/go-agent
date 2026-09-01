package retrieval

import (
	"context"
	"errors"
	"strings"
	"testing"

	postgresstore "github.com/phlin/go-agent/internal/adapters/storage/postgres"
	"github.com/phlin/go-agent/internal/testsupport"
	"github.com/phlin/go-agent/internal/application/ports"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

type vectorMemoryFake struct {
	records  []memorydomain.MemoryRecord
	err      error
	observed *ports.MemoryQuery
}

func (f vectorMemoryFake) StoreMemory(context.Context, memorydomain.MemoryRecord) error { return nil }
func (f vectorMemoryFake) SearchMemories(_ context.Context, query ports.MemoryQuery, _ float64) ([]memorydomain.MemoryRecord, error) {
	if f.observed != nil {
		*f.observed = query
	}
	return f.records, f.err
}

type vectorMemeFake struct {
	results []mediadomain.MemeSearchResult
	err     error
}

func (f vectorMemeFake) IndexMeme(context.Context, string, string, int64) error { return nil }
func (f vectorMemeFake) IndexMemeVersioned(context.Context, string, string, int64, int64) error {
	return nil
}
func (f vectorMemeFake) SearchMemes(context.Context, int64, string, int, float64) ([]mediadomain.MemeSearchResult, error) {
	return f.results, f.err
}

type failingMemoryStore struct {
	*postgresstore.Store
	err error
}

func (s failingMemoryStore) QueryMemories(context.Context, ports.MemoryQuery) ([]memorydomain.MemoryRecord, error) {
	return nil, s.err
}

type failingMemeStore struct {
	*postgresstore.Store
	err error
}

func (s failingMemeStore) SearchMemes(context.Context, ports.MemeQuery) ([]mediadomain.MemeSearchResult, error) {
	return nil, s.err
}

func TestSearchMemoriesUsesBM25AndVectorWithRRF(t *testing.T) {
	store := testsupport.NewStore(t)
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
	store := testsupport.NewStore(t)
	retriever := New(store, store, nil, nil, Config{})
	_, err := retriever.SearchMemories(context.Background(), ports.MemoryQuery{GroupID: 1, UserID: 7, Scope: "group:2", Query: "x"})
	if !errors.Is(err, ErrScopeForbidden) {
		t.Fatalf("expected scope error, got %v", err)
	}
}

func TestSearchMemoriesDegradesWhenVectorFails(t *testing.T) {
	store := testsupport.NewStore(t)
	if err := store.UpsertMemory(context.Background(), memorydomain.MemoryRecord{MemoryID: "lexical", Scope: "group:1", Content: "旧梗"}); err != nil {
		t.Fatal(err)
	}
	retriever := New(store, store, vectorMemoryFake{err: errors.New("embedding unavailable")}, nil, Config{})
	results, err := retriever.SearchMemories(context.Background(), ports.MemoryQuery{GroupID: 1, Query: "旧梗", TopK: 1})
	if err != nil || len(results) != 1 || results[0].MemoryID != "lexical" {
		t.Fatalf("unexpected lexical fallback: results=%+v err=%v", results, err)
	}
}

func TestSearchMemoriesPassesCompleteQueryToVectorTrack(t *testing.T) {
	store := testsupport.NewStore(t)
	var observed ports.MemoryQuery
	retriever := New(store, store, vectorMemoryFake{observed: &observed}, nil, Config{MemoryCandidateK: 12})
	_, err := retriever.SearchMemories(context.Background(), ports.MemoryQuery{
		GroupID: 1,
		UserID:  7,
		Scope:   "group:1:user:7",
		Types:   []string{"user_catchphrase"},
		Query:   "口头禅",
		TopK:    2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.GroupID != 1 || observed.UserID != 7 || observed.Scope != "group:1:user:7" || observed.TopK != 12 || len(observed.Types) != 1 || observed.Types[0] != "user_catchphrase" {
		t.Fatalf("vector query lost constraints: %+v", observed)
	}
}

func TestSearchMemoriesDegradesToVectorWhenLexicalFails(t *testing.T) {
	base := testsupport.NewStore(t)
	store := failingMemoryStore{Store: base, err: errors.New("lexical unavailable")}
	retriever := New(store, base, vectorMemoryFake{records: []memorydomain.MemoryRecord{{MemoryID: "semantic", Scope: "group:1", Content: "语义结果"}}}, nil, Config{})
	results, err := retriever.SearchMemories(context.Background(), ports.MemoryQuery{GroupID: 1, Query: "语义", TopK: 1})
	if err != nil || len(results) != 1 || results[0].MemoryID != "semantic" {
		t.Fatalf("unexpected semantic fallback: results=%+v err=%v", results, err)
	}
}

func TestSearchMemoriesReturnsCombinedErrorWhenAllTracksFail(t *testing.T) {
	base := testsupport.NewStore(t)
	store := failingMemoryStore{Store: base, err: errors.New("lexical unavailable")}
	retriever := New(store, base, vectorMemoryFake{err: errors.New("vector unavailable")}, nil, Config{})
	_, err := retriever.SearchMemories(context.Background(), ports.MemoryQuery{GroupID: 1, Query: "失败", TopK: 1})
	if err == nil || !strings.Contains(err.Error(), "memory lexical search") || !strings.Contains(err.Error(), "memory vector search") {
		t.Fatalf("expected both track errors, got %v", err)
	}
}

func TestSearchMemesDegradesToVectorWhenLexicalFails(t *testing.T) {
	base := testsupport.NewStore(t)
	store := failingMemeStore{Store: base, err: errors.New("lexical unavailable")}
	retriever := New(base, store, nil, vectorMemeFake{results: []mediadomain.MemeSearchResult{{MemeID: "semantic"}}}, Config{})
	results, err := retriever.SearchMemes(context.Background(), ports.MemeQuery{Query: "语义", TopK: 1})
	if err != nil || len(results) != 1 || results[0].MemeID != "semantic" || results[0].MatchType != "vector" {
		t.Fatalf("unexpected meme semantic fallback: results=%+v err=%v", results, err)
	}
}

func TestSearchMemesReturnsCombinedErrorWhenAllTracksFail(t *testing.T) {
	base := testsupport.NewStore(t)
	store := failingMemeStore{Store: base, err: errors.New("lexical unavailable")}
	retriever := New(base, store, nil, vectorMemeFake{err: errors.New("vector unavailable")}, Config{})
	_, err := retriever.SearchMemes(context.Background(), ports.MemeQuery{Query: "失败", TopK: 1})
	if err == nil || !strings.Contains(err.Error(), "meme lexical search") || !strings.Contains(err.Error(), "meme group vector search") {
		t.Fatalf("expected both track errors, got %v", err)
	}
}
