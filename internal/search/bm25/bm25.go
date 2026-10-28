package bm25

import (
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// Document is the minimal input needed by the BM25 scorer.
type Document struct {
	ID   string
	Text string
}

type Result struct {
	ID    string
	Score float64
}

type indexedDocument struct {
	id     string
	length int
	terms  map[string]int
}

// Index is an immutable, reusable BM25 index. Building it tokenizes and
// normalizes every document once; ranked queries only tokenize the query.
type Index struct {
	mu        sync.RWMutex
	documents []indexedDocument
	df        map[string]int
	average   float64
}

// NewIndex builds a reusable index. The input documents are copied, so callers
// may reuse or discard the slice after construction.
func NewIndex(documents []Document) *Index {
	index := &Index{
		documents: make([]indexedDocument, 0, len(documents)),
		df:        make(map[string]int),
	}
	totalLength := 0
	for _, document := range documents {
		terms := tokenize(document.Text)
		counts := make(map[string]int, len(terms))
		for _, term := range terms {
			counts[term]++
		}
		index.documents = append(index.documents, indexedDocument{id: document.ID, length: len(terms), terms: counts})
		totalLength += len(terms)
		for term := range counts {
			index.df[term]++
		}
	}
	if len(index.documents) > 0 {
		index.average = float64(totalLength) / float64(len(index.documents))
	}
	return index
}

// Rank is retained as the small-input compatibility API. Reuse NewIndex when
// ranking more than one query over the same corpus.
func Rank(query string, documents []Document, limit int) []Result {
	return NewIndex(documents).Rank(query, limit)
}

// Rank scores the indexed documents. It is safe for concurrent readers.
func (i *Index) Rank(query string, limit int) []Result {
	if i == nil {
		return nil
	}
	queryTerms := tokenize(query)
	if len(queryTerms) == 0 {
		return nil
	}

	i.mu.RLock()
	defer i.mu.RUnlock()

	if len(i.documents) == 0 || i.average == 0 {
		return nil
	}
	queryFrequency := make(map[string]int, len(queryTerms))
	for _, term := range queryTerms {
		queryFrequency[term]++
	}

	const (
		k1 = 1.2
		b  = 0.75
	)
	scored := make([]Result, 0, len(i.documents))
	for _, document := range i.documents {
		score := 0.0
		for term, qf := range queryFrequency {
			tf := document.terms[term]
			if tf == 0 {
				continue
			}
			df := i.df[term]
			idf := math.Log(1 + (float64(len(i.documents)-df)+0.5)/(float64(df)+0.5))
			normalizedLength := 1 - b + b*float64(document.length)/i.average
			score += float64(qf) * idf * (float64(tf) * (k1 + 1)) / (float64(tf) + k1*normalizedLength)
		}
		if score > 0 {
			scored = append(scored, Result{ID: document.id, Score: score})
		}
	}

	// scored 按文档序构建，SliceStable 保证同分保持原顺序，无需额外 order 字段。
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})
	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}
	return scored
}

func tokenize(text string) []string {
	text = strings.ToLower(text)
	runes := []rune(text)
	terms := make([]string, 0, len(runes))
	for i := 0; i < len(runes); {
		r := runes[i]
		if unicode.Is(unicode.Han, r) {
			terms = append(terms, string(r))
			if i+1 < len(runes) && unicode.Is(unicode.Han, runes[i+1]) {
				terms = append(terms, string(runes[i:i+2]))
			}
			i++
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			start := i
			for i < len(runes) && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i])) && !unicode.Is(unicode.Han, runes[i]) {
				i++
			}
			terms = append(terms, string(runes[start:i]))
			continue
		}
		i++
	}
	return terms
}
