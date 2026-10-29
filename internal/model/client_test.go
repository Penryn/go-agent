package model

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestJSONRejectsTrailingOrUnknownData(t *testing.T) {
	var v struct {
		Action string `json:"action"`
	}
	for _, raw := range []string{`{"action":"text"} {}`, `{"action":"text","tool":"shell"}`, `not-json`} {
		if JSON(raw, &v) == nil {
			t.Fatal(raw)
		}
	}
	if err := JSON("```json\n{\"action\":\"silent\"}\n```", &v); err != nil {
		t.Fatal(err)
	}
}
func TestConcurrencyLimitAndCancellation(t *testing.T) {
	c := New(Config{}, 1, 1, 1, 1)
	release, err := c.acquire(context.Background(), requestChat)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.acquire(ctx, requestChat); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	release()
	if release, err := c.acquire(context.Background(), requestChat); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
}

func TestPoolsAreIsolated(t *testing.T) {
	c := New(Config{}, 1, 1, 1, 1)
	chatRelease, err := c.acquire(context.Background(), requestChat)
	if err != nil {
		t.Fatal(err)
	}
	// A saturated chat pool must not block embedding, background or vision.
	for _, kind := range []requestKind{requestEmbedding, requestBackground, requestVision} {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		release, err := c.acquire(ctx, kind)
		cancel()
		if err != nil {
			t.Fatalf("kind %d waited on another pool: %v", kind, err)
		}
		release()
	}
	// A saturated embedding pool must not consume chat capacity.
	embeddingRelease, err := c.acquire(context.Background(), requestEmbedding)
	if err != nil {
		t.Fatal(err)
	}
	defer embeddingRelease()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.acquire(ctx, requestEmbedding); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("embedding pool did not block at its own limit")
	}
	chatRelease()
	release, err := c.acquire(context.Background(), requestChat)
	if err != nil {
		t.Fatalf("chat pool blocked by embedding: %v", err)
	}
	release()
}
