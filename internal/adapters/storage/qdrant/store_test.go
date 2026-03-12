package qdrantstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/schema"

	"github.com/phlin/go-agent/internal/config"
)

type fakeEmbedder struct{}

func (fakeEmbedder) EmbedStrings(_ context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	result := make([][]float64, 0, len(texts))
	for _, text := range texts {
		vector := []float64{0, 0, 0, 0}
		for i, r := range []rune(text) {
			vector[i%4] += float64(r%17) / 17
		}
		result = append(result, vector)
	}
	return result, nil
}

func TestStoreIntegration(t *testing.T) {
	ctx := context.Background()
	cfg := config.QdrantConfig{
		URL:        "http://127.0.0.1:6334",
		Collection: fmt.Sprintf("qqbot_memories_test_%d", time.Now().UnixNano()),
	}
	store, err := New(ctx, cfg, fakeEmbedder{}, 4, 3)
	if err != nil {
		t.Skipf("qdrant unavailable: %v", err)
	}
	defer store.Close()

	if err := store.Ping(ctx); err != nil {
		t.Fatalf("ping qdrant: %v", err)
	}

	_, err = store.StoreDocuments(ctx, []*schema.Document{
		{ID: "11111111-1111-1111-1111-111111111111", Content: "群里都在聊旧梗"},
		{ID: "22222222-2222-2222-2222-222222222222", Content: "今天先安静一点"},
	})
	if err != nil {
		t.Fatalf("store docs: %v", err)
	}

	docs, err := store.Retrieve(ctx, "旧梗", 2)
	if err != nil {
		t.Fatalf("retrieve docs: %v", err)
	}
	if len(docs) == 0 {
		t.Fatalf("expected qdrant retrieval results")
	}
}
