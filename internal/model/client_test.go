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
	c := New(Config{}, 1)
	release, err := c.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	release()
	if release, err := c.acquire(context.Background()); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
}
