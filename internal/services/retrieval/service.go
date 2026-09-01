package retrieval

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/phlin/go-agent/internal/core/ports"
	searchcore "github.com/phlin/go-agent/internal/core/search"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

var ErrScopeForbidden = errors.New("memory scope is not visible in the current session")

type Config struct {
	MemoryCandidateK int
	MemeCandidateK   int
	RRFK             float64
	// MemoryWeight is a compatibility alias for LexicalWeight.
	MemoryWeight    float64
	LexicalWeight   float64
	VectorWeight    float64
	MemoryThreshold float64
	MemeThreshold   float64
}

type Service struct {
	memoryStore   ports.MemoryStore
	memeStore     ports.MemeStore
	memoryVector  ports.VectorMemoryStore
	memeVector    ports.VectorMemeStore
	memoryLexical ports.LexicalMemoryStore
	memeLexical   ports.LexicalMemeStore
	cfg           Config
}

type rankedMemory struct {
	record memorydomain.MemoryRecord
	score  float64
	order  int
}

type Option func(*Service)

// WithLexicalAdapters injects the BM25/index implementation explicitly. This
// keeps retrieval independent from how authoritative stores build the lexical
// projection and avoids runtime type assertions on store implementations.
func WithLexicalAdapters(memory ports.LexicalMemoryStore, meme ports.LexicalMemeStore) Option {
	return func(s *Service) {
		s.memoryLexical = memory
		s.memeLexical = meme
	}
}

func New(memoryStore ports.MemoryStore, memeStore ports.MemeStore, memoryVector ports.VectorMemoryStore, memeVector ports.VectorMemeStore, cfg Config, opts ...Option) *Service {
	if cfg.MemoryCandidateK <= 0 {
		cfg.MemoryCandidateK = 30
	}
	if cfg.MemeCandidateK <= 0 {
		cfg.MemeCandidateK = 30
	}
	if cfg.RRFK <= 0 {
		cfg.RRFK = 60
	}
	if cfg.LexicalWeight <= 0 {
		cfg.LexicalWeight = cfg.MemoryWeight
	}
	if cfg.LexicalWeight <= 0 {
		cfg.LexicalWeight = 1
	}
	if cfg.VectorWeight <= 0 {
		cfg.VectorWeight = 1
	}
	svc := &Service{memoryStore: memoryStore, memeStore: memeStore, memoryVector: memoryVector, memeVector: memeVector, cfg: cfg}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

func (s *Service) SearchMemories(ctx context.Context, query ports.MemoryQuery) ([]memorydomain.MemoryRecord, error) {
	query.Query = strings.TrimSpace(query.Query)
	if query.Scope != "" && !searchcore.MemoryScopeVisible(query.Scope, query.GroupID, query.UserID) {
		return nil, ErrScopeForbidden
	}
	if query.TopK <= 0 {
		query.TopK = 5
	}
	candidateK := max(query.TopK*5, s.cfg.MemoryCandidateK)
	var lexical []memorydomain.MemoryRecord
	var semantic []memorydomain.MemoryRecord
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		q := query
		q.TopK = candidateK
		if s.memoryLexical != nil {
			lexical, err = s.memoryLexical.SearchMemoriesLexical(gctx, q)
		} else {
			lexical, err = s.memoryStore.QueryMemories(gctx, q)
		}
		return err
	})
	g.Go(func() error {
		if s.memoryVector == nil || query.Query == "" {
			return nil
		}
		var err error
		semantic, err = s.memoryVector.SearchMemories(gctx, query.Query, query.GroupID, query.UserID, candidateK, s.cfg.MemoryThreshold)
		if err != nil {
			slog.WarnContext(gctx, "retrieval: memory vector search failed, using BM25 only", "err", err)
		}
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return mergeMemoryResults(lexical, semantic, query.TopK, s.cfg.LexicalWeight, s.cfg.VectorWeight, s.cfg.RRFK), nil
}

func (s *Service) SearchMemes(ctx context.Context, query ports.MemeQuery) ([]mediadomain.MemeSearchResult, error) {
	query.Query = strings.TrimSpace(query.Query)
	if query.TopK <= 0 {
		query.TopK = 5
	}
	candidateK := max(query.TopK*5, s.cfg.MemeCandidateK)
	var lexical []mediadomain.MemeSearchResult
	var groupSemantic []mediadomain.MemeSearchResult
	var globalSemantic []mediadomain.MemeSearchResult
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		q := query
		q.TopK = candidateK
		if s.memeLexical != nil {
			lexical, err = s.memeLexical.SearchMemesLexical(gctx, q)
		} else {
			lexical, err = s.memeStore.SearchMemes(gctx, q)
		}
		return err
	})
	if s.memeVector != nil && query.Query != "" {
		g.Go(func() error {
			var err error
			groupSemantic, err = s.memeVector.SearchMemes(gctx, query.GroupID, query.Query, candidateK, s.cfg.MemeThreshold)
			if err != nil {
				slog.WarnContext(gctx, "retrieval: meme group vector search failed, using lexical candidates", "err", err)
			}
			return nil
		})
		if query.GroupID != 0 {
			g.Go(func() error {
				var err error
				globalSemantic, err = s.memeVector.SearchMemes(gctx, 0, query.Query, candidateK, s.cfg.MemeThreshold)
				if err != nil {
					slog.WarnContext(gctx, "retrieval: meme global vector search failed", "err", err)
				}
				return nil
			})
		}
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	semantic := mergeVectorMemeCandidates(groupSemantic, globalSemantic)
	return mergeMemeResults(lexical, semantic, candidateK, s.cfg.LexicalWeight, s.cfg.VectorWeight, s.cfg.RRFK), nil
}

func (s *Service) SearchMemesLexical(ctx context.Context, query ports.MemeQuery) ([]mediadomain.MemeSearchResult, error) {
	if query.TopK <= 0 {
		query.TopK = 5
	}
	query.TopK = max(query.TopK*5, s.cfg.MemeCandidateK)
	if s.memeLexical != nil {
		return s.memeLexical.SearchMemesLexical(ctx, query)
	}
	return s.memeStore.SearchMemes(ctx, query)
}

func mergeVectorMemeCandidates(group, global []mediadomain.MemeSearchResult) []mediadomain.MemeSearchResult {
	results := append([]mediadomain.MemeSearchResult(nil), group...)
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		seen[result.MemeID] = struct{}{}
	}
	for _, result := range global {
		if _, ok := seen[result.MemeID]; ok {
			continue
		}
		seen[result.MemeID] = struct{}{}
		results = append(results, result)
	}
	// Both calls are independently ranked by vector similarity. Re-sort the
	// combined visibility partitions before assigning one RRF rank sequence.
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results
}

func mergeMemoryResults(lexical, semantic []memorydomain.MemoryRecord, limit int, lexicalWeight, vectorWeight, rrfK float64) []memorydomain.MemoryRecord {
	byID := make(map[string]*rankedMemory, len(lexical)+len(semantic))
	add := func(records []memorydomain.MemoryRecord, weight float64) {
		for rank, record := range records {
			id := record.MemoryID
			if id == "" {
				id = record.Scope + "\x00" + record.Subject + "\x00" + record.Content
			}
			item := byID[id]
			if item == nil {
				item = &rankedMemory{record: record, order: len(byID)}
				byID[id] = item
			}
			item.score += weight / (rrfK + float64(rank+1))
			if item.record.Content == "" && record.Content != "" {
				item.record = record
			}
		}
	}
	add(lexical, lexicalWeight)
	add(semantic, vectorWeight)
	items := make([]rankedMemory, 0, len(byID))
	for _, item := range byID {
		items = append(items, *item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].score == items[j].score {
			return items[i].order < items[j].order
		}
		return items[i].score > items[j].score
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	results := make([]memorydomain.MemoryRecord, len(items))
	for i := range items {
		results[i] = items[i].record
	}
	return results
}

func MergeMemoryResults(lexical, semantic []memorydomain.MemoryRecord, limit int) []memorydomain.MemoryRecord {
	return mergeMemoryResults(lexical, semantic, limit, 1, 1, 60)
}

func mergeMemeResults(lexical, semantic []mediadomain.MemeSearchResult, limit int, lexicalWeight, vectorWeight, rrfK float64) []mediadomain.MemeSearchResult {
	type ranked struct {
		result  mediadomain.MemeSearchResult
		score   float64
		order   int
		lexical bool
		vector  bool
	}
	byID := make(map[string]*ranked, len(lexical)+len(semantic))
	add := func(results []mediadomain.MemeSearchResult, weight float64, isLexical bool) {
		for rank, result := range results {
			item := byID[result.MemeID]
			if item == nil {
				item = &ranked{result: result, order: len(byID)}
				byID[result.MemeID] = item
			}
			item.score += weight / (rrfK + float64(rank+1))
			if isLexical {
				item.lexical = true
			} else {
				item.vector = true
			}
			if item.result.Descriptor.Summary == "" && result.Descriptor.Summary != "" {
				item.result = result
			}
		}
	}
	add(lexical, lexicalWeight, true)
	add(semantic, vectorWeight, false)
	items := make([]ranked, 0, len(byID))
	for _, item := range byID {
		item.result.Score = item.score
		switch {
		case item.lexical && item.vector:
			item.result.MatchType = "hybrid"
		case item.vector:
			item.result.MatchType = "vector"
		case item.lexical:
			item.result.MatchType = "bm25"
		}
		items = append(items, *item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].score == items[j].score {
			return items[i].order < items[j].order
		}
		return items[i].score > items[j].score
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	results := make([]mediadomain.MemeSearchResult, len(items))
	for i := range items {
		results[i] = items[i].result
	}
	return results
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}
