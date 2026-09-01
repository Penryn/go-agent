package bm25

import (
	"math"
	"sort"
	"strings"
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

// Rank scores all documents with BM25. The tokenizer deliberately keeps its
// contract small so a production Chinese analyzer can replace it later.
func Rank(query string, documents []Document, limit int) []Result {
	queryTerms := tokenize(query)
	if len(queryTerms) == 0 || len(documents) == 0 {
		return nil
	}

	type scoredDocument struct {
		result Result
		order  int
	}
	docTerms := make([]map[string]int, len(documents))
	docLengths := make([]int, len(documents))
	documentFrequency := make(map[string]int)
	totalLength := 0
	for i, document := range documents {
		terms := tokenize(document.Text)
		counts := make(map[string]int, len(terms))
		for _, term := range terms {
			counts[term]++
		}
		docTerms[i] = counts
		docLengths[i] = len(terms)
		totalLength += len(terms)
		for term := range counts {
			documentFrequency[term]++
		}
	}

	avgLength := float64(totalLength) / float64(len(documents))
	if avgLength == 0 {
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
	scored := make([]Result, 0, len(documents))
	for i, terms := range docTerms {
		score := 0.0
		for term, qf := range queryFrequency {
			tf := terms[term]
			if tf == 0 {
				continue
			}
			df := documentFrequency[term]
			idf := math.Log(1 + (float64(len(documents)-df)+0.5)/(float64(df)+0.5))
			normalizedLength := 1 - b + b*float64(docLengths[i])/avgLength
			score += float64(qf) * idf * (float64(tf) * (k1 + 1)) / (float64(tf) + k1*normalizedLength)
		}
		if score > 0 {
			scored = append(scored, Result{ID: documents[i].ID, Score: score})
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
