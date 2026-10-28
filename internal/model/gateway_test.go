package model

import (
	"context"
	"testing"
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
