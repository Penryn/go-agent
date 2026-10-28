package model

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/phlin/go-agent/internal/cache"
)

const (
	defaultEmbeddingCacheItems = 4096
	defaultEmbeddingCacheTTL   = 30 * time.Minute
)

// CachedEmbedder canonicalizes text and coalesces identical embedding requests
// across all services that share the same model configuration.
type CachedEmbedder struct {
	base  Embedder
	cache *cache.Cache[[]float32]
	ttl   time.Duration
}

func NewCachedEmbedder(base Embedder, maxItems int, ttl time.Duration) *CachedEmbedder {
	if base == nil {
		return nil
	}
	if maxItems < 1 {
		maxItems = defaultEmbeddingCacheItems
	}
	if ttl <= 0 {
		ttl = defaultEmbeddingCacheTTL
	}
	return &CachedEmbedder{
		base: base,
		cache: cache.New(maxItems, func(value []float32) []float32 {
			return append([]float32(nil), value...)
		}),
		ttl: ttl,
	}
}

func (e *CachedEmbedder) EmbeddingVersion() string {
	if e == nil || e.base == nil {
		return ""
	}
	return e.base.EmbeddingVersion()
}

func (e *CachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	normalized := normalizeEmbeddingText(text)
	sum := sha256.Sum256([]byte(normalized))
	key := fmt.Sprintf("%s:%x", e.EmbeddingVersion(), sum[:])
	value, _, err := e.cache.Load(ctx, key, e.ttl, func(call context.Context) ([]float32, error) {
		return e.base.Embed(call, normalized)
	})
	if err != nil {
		return nil, err
	}
	return append([]float32(nil), value...), nil
}

func (e *CachedEmbedder) Stats() cache.Stats {
	if e == nil || e.cache == nil {
		return cache.Stats{}
	}
	return e.cache.Stats()
}

func normalizeEmbeddingText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}
