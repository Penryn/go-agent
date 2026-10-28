package model

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countingEmbedder struct {
	version atomic.Value
	calls   atomic.Int32
	delay   time.Duration
}

func (e *countingEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	e.calls.Add(1)
	if e.delay > 0 {
		select {
		case <-time.After(e.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return []float32{1, 2, 3}, nil
}

func (e *countingEmbedder) EmbeddingVersion() string {
	return e.version.Load().(string)
}

func TestCachedEmbedderCanonicalizesAndCoalesces(t *testing.T) {
	base := &countingEmbedder{delay: 20 * time.Millisecond}
	base.version.Store("v1")
	cached := NewCachedEmbedder(base, 8, time.Minute)

	const calls = 16
	var wg sync.WaitGroup
	values := make(chan []float32, calls)
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := cached.Embed(context.Background(), "  Hello \n WORLD ")
			if err != nil {
				t.Error(err)
				return
			}
			values <- value
		}()
	}
	wg.Wait()
	close(values)
	if base.calls.Load() != 1 {
		t.Fatalf("embedding called %d times", base.calls.Load())
	}
	for value := range values {
		value[0] = 99
	}
	again, err := cached.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if again[0] != 1 || base.calls.Load() != 1 {
		t.Fatalf("cached slice was mutated: %v calls=%d", again, base.calls.Load())
	}
}

func TestCachedEmbedderSeparatesModelVersions(t *testing.T) {
	base := &countingEmbedder{}
	base.version.Store("v1")
	cached := NewCachedEmbedder(base, 8, time.Minute)
	if _, err := cached.Embed(context.Background(), "same"); err != nil {
		t.Fatal(err)
	}
	base.version.Store("v2")
	if _, err := cached.Embed(context.Background(), "same"); err != nil {
		t.Fatal(err)
	}
	if base.calls.Load() != 2 {
		t.Fatalf("model version did not invalidate cache: calls=%d", base.calls.Load())
	}
}
