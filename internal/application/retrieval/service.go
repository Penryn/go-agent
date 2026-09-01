package retrieval

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/phlin/go-agent/internal/application/ports"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	searchcore "github.com/phlin/go-agent/internal/search"
)

var ErrScopeForbidden = errors.New("memory scope is not visible in the current session")

const rrfK = 60.0

type Config struct {
	MemoryCandidateK int
	MemeCandidateK   int
	MemoryThreshold  float64
	MemeThreshold    float64
}

type Service struct {
	memoryStore  ports.MemoryStore
	memeStore    ports.MemeStore
	memoryVector ports.VectorMemoryStore
	memeVector   ports.VectorMemeStore
	cfg          Config
}

type rankedMemory struct {
	record memorydomain.MemoryRecord
	score  float64
	order  int
}

func New(memoryStore ports.MemoryStore, memeStore ports.MemeStore, memoryVector ports.VectorMemoryStore, memeVector ports.VectorMemeStore, cfg Config) *Service {
	if cfg.MemoryCandidateK <= 0 {
		cfg.MemoryCandidateK = 30
	}
	if cfg.MemeCandidateK <= 0 {
		cfg.MemeCandidateK = 30
	}
	return &Service{memoryStore: memoryStore, memeStore: memeStore, memoryVector: memoryVector, memeVector: memeVector, cfg: cfg}
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
		lexical, err = s.memoryStore.QueryMemories(gctx, q)
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
	return mergeMemoryResults(lexical, semantic, query.TopK), nil
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
		lexical, err = s.memeStore.SearchMemes(gctx, q)
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
	return mergeMemeResults(lexical, semantic, candidateK), nil
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

func mergeMemoryResults(lexical, semantic []memorydomain.MemoryRecord, limit int) []memorydomain.MemoryRecord {
	byID := make(map[string]*rankedMemory, len(lexical)+len(semantic))
	add := func(records []memorydomain.MemoryRecord) {
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
			item.score += 1 / (rrfK + float64(rank+1))
			if item.record.Content == "" && record.Content != "" {
				item.record = record
			}
		}
	}
	add(lexical)
	add(semantic)
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

func mergeMemeResults(lexical, semantic []mediadomain.MemeSearchResult, limit int) []mediadomain.MemeSearchResult {
	type ranked struct {
		result  mediadomain.MemeSearchResult
		score   float64
		order   int
		lexical bool
		vector  bool
	}
	byID := make(map[string]*ranked, len(lexical)+len(semantic))
	add := func(results []mediadomain.MemeSearchResult, isLexical bool) {
		for rank, result := range results {
			item := byID[result.MemeID]
			if item == nil {
				item = &ranked{result: result, order: len(byID)}
				byID[result.MemeID] = item
			}
			item.score += 1 / (rrfK + float64(rank+1))
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
	add(lexical, true)
	add(semantic, false)
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
