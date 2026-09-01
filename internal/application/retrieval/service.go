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

// mergeRRF 用 Reciprocal Rank Fusion 融合 lexical 与 semantic 两轨结果:
// score += 1/(rrfK + rank+1),按融合分稳定排序后截断。better 在两轨给出
// 同一 ID 的不同投影时挑选更完整的那条(取首个非空 summary/content)。
func mergeRRF[T any](lexical, semantic []T, limit int, id func(T) string, better func(cur, next T) T) []T {
	type ranked struct {
		item  T
		score float64
		order int
	}
	byID := make(map[string]*ranked, len(lexical)+len(semantic))
	add := func(items []T) {
		for rank, item := range items {
			key := id(item)
			if key == "" {
				continue
			}
			entry := byID[key]
			if entry == nil {
				entry = &ranked{item: item, order: len(byID)}
				byID[key] = entry
			}
			entry.score += 1 / (rrfK + float64(rank+1))
			entry.item = better(entry.item, item)
		}
	}
	add(lexical)
	add(semantic)
	items := make([]ranked, 0, len(byID))
	for _, entry := range byID {
		items = append(items, *entry)
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
	results := make([]T, len(items))
	for i := range items {
		results[i] = items[i].item
	}
	return results
}

func mergeMemoryResults(lexical, semantic []memorydomain.MemoryRecord, limit int) []memorydomain.MemoryRecord {
	return mergeRRF(lexical, semantic, limit,
		func(r memorydomain.MemoryRecord) string {
			if r.MemoryID != "" {
				return r.MemoryID
			}
			return r.Scope + "\x00" + r.Subject + "\x00" + r.Content
		},
		func(cur, next memorydomain.MemoryRecord) memorydomain.MemoryRecord {
			if cur.Content == "" && next.Content != "" {
				return next
			}
			return cur
		},
	)
}

func mergeMemeResults(lexical, semantic []mediadomain.MemeSearchResult, limit int) []mediadomain.MemeSearchResult {
	results := mergeRRF(lexical, semantic, limit,
		func(r mediadomain.MemeSearchResult) string { return r.MemeID },
		func(cur, next mediadomain.MemeSearchResult) mediadomain.MemeSearchResult {
			if cur.Descriptor.Summary == "" && next.Descriptor.Summary != "" {
				return next
			}
			return cur
		},
	)
	lexIDs := make(map[string]struct{}, len(lexical))
	for _, item := range lexical {
		lexIDs[item.MemeID] = struct{}{}
	}
	for i := range results {
		if _, lexicalHit := lexIDs[results[i].MemeID]; lexicalHit {
			results[i].MatchType = "hybrid"
		} else {
			results[i].MatchType = "vector"
		}
	}
	return results
}
