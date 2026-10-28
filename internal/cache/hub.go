package cache

import (
	"context"
	"encoding/json"
	"time"
)

// CacheHub is the process-local cache boundary used by application services.
// Values are encoded when crossing the boundary so callers cannot mutate the
// cached representation. Implementations may be replaced by Redis later.
type CacheHub interface {
	Get(key string, dst any) bool
	Set(key string, value any, ttl time.Duration)
	Delete(key string)
	Do(ctx context.Context, key string, fn func(context.Context) (any, error)) (any, error)
}

type Hub struct {
	values *Cache[[]byte]
}

func NewHub(maxItems int) *Hub {
	return &Hub{values: New(maxItems, func(value []byte) []byte { return append([]byte(nil), value...) })}
}

func (h *Hub) Get(key string, dst any) bool {
	if h == nil || h.values == nil || dst == nil {
		return false
	}
	b, ok := h.values.Get(key)
	if !ok || json.Unmarshal(b, dst) != nil {
		return false
	}
	return true
}

func (h *Hub) Set(key string, value any, ttl time.Duration) {
	if h == nil || h.values == nil {
		return
	}
	b, err := json.Marshal(value)
	if err != nil {
		return
	}
	h.values.Set(key, b, ttl)
}

func (h *Hub) Delete(key string) {
	if h != nil && h.values != nil {
		h.values.Forget(key)
	}
}

func (h *Hub) Do(ctx context.Context, key string, fn func(context.Context) (any, error)) (any, error) {
	return h.DoTTL(ctx, key, 24*time.Hour, fn)
}

// DoTTL is Do with an explicit lifetime for the computed value. A non-positive
// lifetime still coalesces concurrent callers but does not retain the result.
// Keeping the lifetime at the cache boundary prevents callers from silently
// inheriting the old one-day default when a shorter freshness window is needed.
func (h *Hub) DoTTL(ctx context.Context, key string, ttl time.Duration, fn func(context.Context) (any, error)) (any, error) {
	if h == nil || h.values == nil {
		return fn(ctx)
	}
	value, _, err := h.values.Load(ctx, key, ttl, func(call context.Context) ([]byte, error) {
		loaded, err := fn(call)
		if err != nil {
			return nil, err
		}
		return json.Marshal(loaded)
	})
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(value, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DoTyped preserves the concrete value type while still using the JSON-backed
// isolation of Hub. It is useful for slices such as embeddings where decoding
// into interface{} would otherwise lose the element type.
func DoTyped[T any](h *Hub, ctx context.Context, key string, ttl time.Duration, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	if h == nil || h.values == nil {
		return fn(ctx)
	}
	value, _, err := h.values.Load(ctx, key, ttl, func(call context.Context) ([]byte, error) {
		loaded, err := fn(call)
		if err != nil {
			return nil, err
		}
		return json.Marshal(loaded)
	})
	if err != nil {
		return zero, err
	}
	var out T
	if err := json.Unmarshal(value, &out); err != nil {
		return zero, err
	}
	return out, nil
}

func (h *Hub) Stats() Stats {
	if h == nil || h.values == nil {
		return Stats{}
	}
	return h.values.Stats()
}
