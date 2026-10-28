package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func cloneBytes(value []byte) []byte {
	return append([]byte(nil), value...)
}

func TestTTLAndLRUEviction(t *testing.T) {
	c := New(2, cloneBytes)
	c.Set("a", []byte("a"), time.Minute)
	c.Set("b", []byte("b"), time.Minute)
	if _, ok := c.Get("a"); !ok {
		t.Fatal("expected a hit")
	}
	c.Set("c", []byte("c"), time.Minute)
	if _, ok := c.Get("b"); ok {
		t.Fatal("least recently used item was not evicted")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("recently used item was evicted")
	}

	c.Set("short", []byte("value"), time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	if _, ok := c.Get("short"); ok {
		t.Fatal("expired item was returned")
	}
}

func TestValuesAreIsolated(t *testing.T) {
	c := New(1, cloneBytes)
	original := []byte("original")
	c.Set("key", original, time.Minute)
	original[0] = 'X'
	first, ok := c.Get("key")
	if !ok || string(first) != "original" {
		t.Fatalf("unexpected cached value %q", first)
	}
	first[0] = 'Y'
	second, ok := c.Get("key")
	if !ok || string(second) != "original" {
		t.Fatalf("caller mutation changed cache: %q", second)
	}
}

func TestLoadDoesNotCacheFailures(t *testing.T) {
	c := New(1, cloneBytes)
	var calls atomic.Int32
	loader := func(context.Context) ([]byte, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("temporary")
		}
		return []byte("ok"), nil
	}
	if _, _, err := c.Load(context.Background(), "key", time.Minute, loader); err == nil {
		t.Fatal("expected first load to fail")
	}
	value, cached, err := c.Load(context.Background(), "key", time.Minute, loader)
	if err != nil || cached || string(value) != "ok" || calls.Load() != 2 {
		t.Fatalf("unexpected retry: value=%q cached=%v calls=%d err=%v", value, cached, calls.Load(), err)
	}
	_, cached, err = c.Load(context.Background(), "key", time.Minute, loader)
	if err != nil || !cached || calls.Load() != 2 {
		t.Fatalf("failed load was cached or hit missing: cached=%v calls=%d err=%v", cached, calls.Load(), err)
	}
}

func TestLoadCoalescesConcurrentRequests(t *testing.T) {
	c := New(1, cloneBytes)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	loader := func(context.Context) ([]byte, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return []byte("shared"), nil
	}

	const callers = 24
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, _, err := c.Load(context.Background(), "same", time.Minute, loader)
			if err == nil && string(value) != "shared" {
				err = errors.New("unexpected value")
			}
			errs <- err
		}()
	}
	<-started
	time.Sleep(10 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("loader called %d times", calls.Load())
	}
}
