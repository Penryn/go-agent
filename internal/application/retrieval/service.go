package retrieval

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

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
	traceStore   ports.RetrievalTraceStore
}

func New(memoryStore ports.MemoryStore, memeStore ports.MemeStore, memoryVector ports.VectorMemoryStore, memeVector ports.VectorMemeStore, cfg Config) *Service {
	if cfg.MemoryCandidateK <= 0 {
		cfg.MemoryCandidateK = 30
	}
	if cfg.MemeCandidateK <= 0 {
		cfg.MemeCandidateK = 30
	}
	traceStore, _ := memoryStore.(ports.RetrievalTraceStore)
	return &Service{memoryStore: memoryStore, memeStore: memeStore, memoryVector: memoryVector, memeVector: memeVector, cfg: cfg, traceStore: traceStore}
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
	var lexicalErr error
	var semanticErr error
	vectorEnabled := s.memoryVector != nil && query.Query != ""
	var g errgroup.Group
	g.Go(func() error {
		q := query
		q.TopK = candidateK
		lexical, lexicalErr = s.memoryStore.QueryMemories(ctx, q)
		return nil
	})
	if vectorEnabled {
		g.Go(func() error {
			q := query
			q.TopK = candidateK
			semantic, semanticErr = s.memoryVector.SearchMemories(ctx, q, s.cfg.MemoryThreshold)
			return nil
		})
	}
	_ = g.Wait()
	if lexicalErr != nil {
		slog.WarnContext(ctx, "retrieval: memory lexical search failed", "err", lexicalErr)
	}
	if semanticErr != nil {
		slog.WarnContext(ctx, "retrieval: memory vector search failed", "err", semanticErr)
	}
	if lexicalErr != nil && (!vectorEnabled || semanticErr != nil) {
		return nil, errors.Join(wrapTrackError("memory lexical search", lexicalErr), wrapTrackError("memory vector search", semanticErr))
	}
	results := mergeMemoryResults(lexical, semantic, query.TopK)
	if s.traceStore != nil && query.EventID != "" {
		seen := make(map[string]struct{}, len(lexical)+len(semantic))
		for _, record := range append(append([]memorydomain.MemoryRecord{}, lexical...), semantic...) {
			if record.MemoryID != "" {
				seen[record.MemoryID] = struct{}{}
			}
		}
		hits := make([]string, 0, len(results))
		for _, record := range results {
			if record.MemoryID != "" {
				hits = append(hits, record.MemoryID)
			}
		}
		traceID := fmt.Sprintf("retrieval-%s-%d", query.TraceID, time.Now().UnixNano())
		if err := s.traceStore.SaveRetrievalTrace(ctx, ports.RetrievalTrace{
			TraceID: traceID, EventID: query.EventID, GroupID: query.GroupID, UserID: query.UserID,
			Query: query.Query, CandidateCount: len(seen), HitMemoryIDs: hits, CreatedAt: time.Now(),
			VectorEnabled: vectorEnabled, VectorError: semanticErr != nil,
		}); err != nil {
			slog.WarnContext(ctx, "retrieval: save trace failed", "err", err)
		}
	}
	slog.DebugContext(ctx, "retrieval: memory search completed",
		"candidate_k", candidateK,
		"lexical_candidates", len(lexical),
		"semantic_candidates", len(semantic),
		"result_count", len(results),
		"vector_degraded", semanticErr != nil,
		"lexical_degraded", lexicalErr != nil,
	)
	return results, nil
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
	var lexicalErr error
	var groupSemanticErr error
	var globalSemanticErr error
	vectorEnabled := s.memeVector != nil && query.Query != ""
	globalVectorEnabled := vectorEnabled && query.GroupID != 0
	var g errgroup.Group
	g.Go(func() error {
		q := query
		q.TopK = candidateK
		lexical, lexicalErr = s.memeStore.SearchMemes(ctx, q)
		return nil
	})
	if vectorEnabled {
		g.Go(func() error {
			groupSemantic, groupSemanticErr = s.memeVector.SearchMemes(ctx, query.GroupID, query.Query, candidateK, s.cfg.MemeThreshold)
			return nil
		})
		if globalVectorEnabled {
			g.Go(func() error {
				globalSemantic, globalSemanticErr = s.memeVector.SearchMemes(ctx, 0, query.Query, candidateK, s.cfg.MemeThreshold)
				return nil
			})
		}
	}
	_ = g.Wait()
	if lexicalErr != nil {
		slog.WarnContext(ctx, "retrieval: meme lexical search failed", "err", lexicalErr)
	}
	if groupSemanticErr != nil {
		slog.WarnContext(ctx, "retrieval: meme group vector search failed", "err", groupSemanticErr)
	}
	if globalSemanticErr != nil {
		slog.WarnContext(ctx, "retrieval: meme global vector search failed", "err", globalSemanticErr)
	}
	semanticAvailable := (vectorEnabled && groupSemanticErr == nil) || (globalVectorEnabled && globalSemanticErr == nil)
	if lexicalErr != nil && !semanticAvailable {
		return nil, errors.Join(
			wrapTrackError("meme lexical search", lexicalErr),
			wrapTrackError("meme group vector search", groupSemanticErr),
			wrapTrackError("meme global vector search", globalSemanticErr),
		)
	}
	semantic := mergeVectorMemeCandidates(groupSemantic, globalSemantic)
	results := mergeMemeResults(lexical, semantic, candidateK)
	slog.DebugContext(ctx, "retrieval: meme search completed",
		"candidate_k", candidateK,
		"lexical_candidates", len(lexical),
		"group_semantic_candidates", len(groupSemantic),
		"global_semantic_candidates", len(globalSemantic),
		"result_count", len(results),
		"vector_degraded", groupSemanticErr != nil || globalSemanticErr != nil,
		"lexical_degraded", lexicalErr != nil,
	)
	return results, nil
}

func wrapTrackError(track string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", track, err)
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

// rrfItem 是 Reciprocal Rank Fusion 的中间结构:score += 1/(rrfK + rank+1)。
type rrfItem[T any] struct {
	item  T
	score float64
	order int
}

// mergeRRF 按 id 融合两轨结果,同 id 时 better 取更完整投影(非空 content/summary)。
func mergeRRF[T any](lexical, semantic []T, limit int, id func(T) string, better func(cur, next T) T) []T {
	byID := make(map[string]*rrfItem[T], len(lexical)+len(semantic))
	for _, items := range [][]T{lexical, semantic} {
		for rank, item := range items {
			key := id(item)
			if key == "" {
				continue
			}
			entry := byID[key]
			if entry == nil {
				entry = &rrfItem[T]{item: item, order: len(byID)}
				byID[key] = entry
			}
			entry.score += 1 / (rrfK + float64(rank+1))
			entry.item = better(entry.item, item)
		}
	}
	items := make([]rrfItem[T], 0, len(byID))
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
