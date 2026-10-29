package cache

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestHubIsolatesValuesAndUsesVersionedKeys(t *testing.T) {
	h := NewHub(4)
	original := map[string]any{"items": []string{"a"}}
	h.Set(RecallKey(7, 2, 3, "v1", " Hello "), original, time.Minute)
	original["items"].([]string)[0] = "changed"
	var got map[string]any
	if !h.Get(RecallKey(7, 2, 3, "v1", "hello"), &got) || got["items"].([]any)[0] != "a" {
		t.Fatalf("cached value was not isolated: %#v", got)
	}
	if h.Get(RecallKey(8, 2, 3, "v1", "hello"), &got) {
		t.Fatal("cache key crossed group boundary")
	}
}

func TestHubDoCoalescesLoads(t *testing.T) {
	h := NewHub(4)
	var calls atomic.Int32
	load := func(context.Context) (any, error) {
		calls.Add(1)
		return map[string]string{"ok": "yes"}, nil
	}
	for i := 0; i < 4; i++ {
		value, err := h.Do(context.Background(), "same", load)
		if err != nil || value == nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("load called %d times", calls.Load())
	}
}

func TestHubDoTypedPreservesSlicesAndTTL(t *testing.T) {
	h := NewHub(2)
	values, err := DoTyped[[]float32](h, context.Background(), "vector", time.Minute, func(context.Context) ([]float32, error) { return []float32{1, 2}, nil })
	if err != nil || len(values) != 2 || values[1] != 2 {
		t.Fatalf("typed value lost: %v %v", values, err)
	}
	values[0] = 9
	again, err := DoTyped[[]float32](h, context.Background(), "vector", time.Minute, func(context.Context) ([]float32, error) { return []float32{3}, nil })
	if err != nil || again[0] != 1 {
		t.Fatalf("cached slice was not isolated: %v", again)
	}
}
