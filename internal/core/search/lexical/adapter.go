package lexical

import (
	"context"
	"fmt"
	"strings"

	"github.com/phlin/go-agent/internal/core/ports"
	"github.com/phlin/go-agent/internal/core/search/bm25"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

// MemoryAdapter applies BM25 to a filtered authoritative corpus. This is an
// interim full-scan implementation; it preserves the seam for replacing it
// with a durable inverted index or a database BM25 extension later.
type MemoryAdapter struct {
	source ports.MemoryCorpusReader
}

func NewMemoryAdapter(source ports.MemoryCorpusReader) *MemoryAdapter {
	return &MemoryAdapter{source: source}
}

func (a *MemoryAdapter) SearchMemoriesLexical(ctx context.Context, query ports.MemoryQuery) ([]memorydomain.MemoryRecord, error) {
	records, err := a.source.ListMemories(ctx, query)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(query.Query) == "" {
		return limitMemories(records, query.TopK), nil
	}
	documents := make([]bm25.Document, len(records))
	byID := make(map[string]memorydomain.MemoryRecord, len(records))
	for i, record := range records {
		id := record.MemoryID
		if id == "" {
			id = fmt.Sprintf("memory-document-%d", i)
		}
		documents[i] = bm25.Document{ID: id, Text: record.Subject + "\n" + record.Content}
		byID[id] = record
	}
	ranked := bm25.Rank(query.Query, documents, query.TopK)
	result := make([]memorydomain.MemoryRecord, 0, len(ranked))
	for _, item := range ranked {
		result = append(result, byID[item.ID])
	}
	return result, nil
}

// MemeAdapter applies the same BM25 implementation to meme descriptors.
type MemeAdapter struct {
	source ports.MemeCorpusReader
}

func NewMemeAdapter(source ports.MemeCorpusReader) *MemeAdapter {
	return &MemeAdapter{source: source}
}

func (a *MemeAdapter) SearchMemesLexical(ctx context.Context, query ports.MemeQuery) ([]mediadomain.MemeSearchResult, error) {
	results, err := a.source.ListMemes(ctx, query)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(query.Query) == "" {
		return limitMemes(results, query.TopK), nil
	}
	documents := make([]bm25.Document, len(results))
	byID := make(map[string]mediadomain.MemeSearchResult, len(results))
	for i, result := range results {
		documents[i] = bm25.Document{ID: result.MemeID, Text: memeText(result.Descriptor)}
		byID[result.MemeID] = result
	}
	ranked := bm25.Rank(query.Query, documents, query.TopK)
	output := make([]mediadomain.MemeSearchResult, 0, len(ranked))
	for _, item := range ranked {
		result := byID[item.ID]
		result.Score = item.Score
		result.MatchType = "bm25"
		output = append(output, result)
	}
	return output, nil
}

func memeText(descriptor mediadomain.MemeDescriptor) string {
	return strings.Join([]string{
		descriptor.Title,
		descriptor.Summary,
		strings.Join(descriptor.Keywords, " "),
		strings.Join(descriptor.EmotionTags, " "),
		strings.Join(descriptor.SceneTags, " "),
		strings.Join(descriptor.UsageHints, " "),
	}, "\n")
}

func limitMemories(records []memorydomain.MemoryRecord, limit int) []memorydomain.MemoryRecord {
	if limit > 0 && len(records) > limit {
		return records[:limit]
	}
	return records
}

func limitMemes(results []mediadomain.MemeSearchResult, limit int) []mediadomain.MemeSearchResult {
	if limit > 0 && len(results) > limit {
		return results[:limit]
	}
	return results
}
