package cache

import (
	"container/list"
	"context"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// Stats is a point-in-time cache counter snapshot.
type Stats struct {
	Hits       uint64 `json:"hits"`
	Misses     uint64 `json:"misses"`
	Loads      uint64 `json:"loads"`
	LoadErrors uint64 `json:"load_errors"`
	Expired    uint64 `json:"expired"`
	Evictions  uint64 `json:"evictions"`
}

type entry[V any] struct {
	key       string
	value     V
	expiresAt time.Time
}

type loaded[V any] struct {
	value V
}

// Cache is a bounded TTL-LRU cache with request coalescing. clone must return
// an independent copy so cached mutable values cannot be modified by callers.
type Cache[V any] struct {
	mu       sync.Mutex
	maxItems int
	entries  map[string]*list.Element
	lru      *list.List
	clone    func(V) V
	group    singleflight.Group
	hits     atomic.Uint64
	misses   atomic.Uint64
	loads    atomic.Uint64
	errs     atomic.Uint64
	expired  atomic.Uint64
	evicted  atomic.Uint64
}

func New[V any](maxItems int, clone func(V) V) *Cache[V] {
	if maxItems < 1 {
		maxItems = 1
	}
	if clone == nil {
		panic("cache: clone function is required")
	}
	return &Cache[V]{
		maxItems: maxItems,
		entries:  make(map[string]*list.Element, maxItems),
		lru:      list.New(),
		clone:    clone,
	}
}

// Get returns an isolated copy and refreshes the key's LRU position.
func (c *Cache[V]) Get(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.getLocked(key, true)
}

func (c *Cache[V]) getLocked(key string, record bool) (V, bool) {
	var zero V
	element, ok := c.entries[key]
	if !ok {
		if record {
			c.misses.Add(1)
		}
		return zero, false
	}
	entry := element.Value.(*entry[V])
	if !entry.expiresAt.IsZero() && !time.Now().Before(entry.expiresAt) {
		delete(c.entries, key)
		c.lru.Remove(element)
		c.expired.Add(1)
		if record {
			c.misses.Add(1)
		}
		return zero, false
	}
	c.lru.MoveToFront(element)
	if record {
		c.hits.Add(1)
	}
	return c.clone(entry.value), true
}

// Set stores an isolated copy. A non-positive TTL stores the value without an
// expiry until it is evicted or forgotten.
func (c *Cache[V]) Set(key string, value V, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setLocked(key, value, ttl)
}

func (c *Cache[V]) setLocked(key string, value V, ttl time.Duration) {
	expiresAt := time.Time{}
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}
	if element, ok := c.entries[key]; ok {
		entry := element.Value.(*entry[V])
		entry.value = c.clone(value)
		entry.expiresAt = expiresAt
		c.lru.MoveToFront(element)
		return
	}
	element := c.lru.PushFront(&entry[V]{key: key, value: c.clone(value), expiresAt: expiresAt})
	c.entries[key] = element
	for c.lru.Len() > c.maxItems {
		oldest := c.lru.Back()
		if oldest == nil {
			break
		}
		old := oldest.Value.(*entry[V])
		delete(c.entries, old.key)
		c.lru.Remove(oldest)
		c.evicted.Add(1)
	}
}

// Load returns a cached value or computes it once for concurrent callers with
// the same key. cached reports whether the returned value came from cache.
func (c *Cache[V]) Load(ctx context.Context, key string, ttl time.Duration, load func(context.Context) (V, error)) (V, bool, error) {
	if value, ok := c.Get(key); ok {
		return value, true, nil
	}
	if ttl <= 0 {
		value, err := load(ctx)
		return value, false, err
	}

	result := c.group.DoChan(key, func() (any, error) {
		if value, ok := c.Get(key); ok {
			return loaded[V]{value: value}, nil
		}
		c.loads.Add(1)
		value, err := load(ctx)
		if err != nil {
			c.errs.Add(1)
			return loaded[V]{}, err
		}
		c.Set(key, value, ttl)
		return loaded[V]{value: value}, nil
	})
	select {
	case <-ctx.Done():
		var zero V
		return zero, false, ctx.Err()
	case outcome := <-result:
		if outcome.Err != nil {
			var zero V
			return zero, false, outcome.Err
		}
		return c.clone(outcome.Val.(loaded[V]).value), false, nil
	}
}

func (c *Cache[V]) Forget(key string) {
	c.mu.Lock()
	if element, ok := c.entries[key]; ok {
		delete(c.entries, key)
		c.lru.Remove(element)
	}
	c.mu.Unlock()
	c.group.Forget(key)
}

func (c *Cache[V]) Purge() {
	c.mu.Lock()
	c.entries = make(map[string]*list.Element, c.maxItems)
	c.lru.Init()
	c.mu.Unlock()
}

func (c *Cache[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}

func (c *Cache[V]) Stats() Stats {
	return Stats{
		Hits:       c.hits.Load(),
		Misses:     c.misses.Load(),
		Loads:      c.loads.Load(),
		LoadErrors: c.errs.Load(),
		Expired:    c.expired.Load(),
		Evictions:  c.evicted.Load(),
	}
}
