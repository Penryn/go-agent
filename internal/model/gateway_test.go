package model

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/cache"
)

type gatewayGenerator struct{}

func (gatewayGenerator) Generate(context.Context, string, any, []string) (Result, error) {
	return Result{Content: "ok"}, nil
}

type gatewayEmbedder struct{}

func (gatewayEmbedder) Embed(context.Context, string) ([]float32, error) { return []float32{1}, nil }
func (gatewayEmbedder) EmbeddingVersion() string                         { return "v1" }

func TestGatewayRequestBoundary(t *testing.T) {
	g := Gateway{Generator: gatewayGenerator{}, Embedder: gatewayEmbedder{}}
	if _, err := g.GenerateRequest(context.Background(), GenerateRequest{System: "system", Input: map[string]string{"x": "y"}}); err != nil {
		t.Fatal(err)
	}
	values, err := g.EmbedRequest(context.Background(), EmbedRequest{Text: "hello", Priority: PriorityInteractive})
	if err != nil || len(values) != 1 {
		t.Fatalf("embedding request failed: %v %v", values, err)
	}
}

func TestGatewayCachesEmbeddingAndObservesRequests(t *testing.T) {
	var calls atomic.Int32
	base := &countingEmbedder{}
	base.version.Store("v1")
	g := NewGateway(gatewayGenerator{}, countingEmbedderAdapter{base: base, calls: &calls}, cache.NewHub(4), time.Minute, 1, 1, 1, 1)
	var observed atomic.Int32
	g.Observe = func(o Observation) {
		if o.Operation == "embedding" {
			observed.Add(1)
		}
	}
	if _, err := g.Embed(context.Background(), " Hello "); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Embed(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("cached embedding called base %d times", calls.Load())
	}
	if observed.Load() != 2 {
		t.Fatalf("missing observations: %d", observed.Load())
	}
}

func TestGatewayObservationsCarryIdentityAndErrorClass(t *testing.T) {
	g := Gateway{Generator: failingGenerator{}, Embedder: gatewayEmbedder{}}
	var got Observation
	g.Observe = func(o Observation) { got = o }
	if _, err := g.GenerateRequest(context.Background(), GenerateRequest{PromptVersion: "chat-v1", GroupID: 7, AttemptID: "turn:7:1", System: "s"}); err == nil {
		t.Fatal("expected the generator failure")
	}
	if got.Operation != "generate" || got.GroupID != 7 || got.AttemptID != "turn:7:1" || got.PromptVersion != "chat-v1" || got.ErrorClass != "provider" {
		t.Fatalf("observation lost identity or error class: %+v", got)
	}
}

type failingGenerator struct{}

func (failingGenerator) Generate(context.Context, string, any, []string) (Result, error) {
	return Result{}, errors.New("provider rejected")
}

type countingEmbedderAdapter struct {
	base  *countingEmbedder
	calls *atomic.Int32
}

func (e countingEmbedderAdapter) Embed(ctx context.Context, text string) ([]float32, error) {
	e.calls.Add(1)
	return e.base.Embed(ctx, text)
}
func (e countingEmbedderAdapter) EmbeddingVersion() string { return e.base.EmbeddingVersion() }
