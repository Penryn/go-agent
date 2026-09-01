package app

import (
	"context"
	"log/slog"

	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
	postgresstore "github.com/phlin/go-agent/internal/adapters/storage/postgres"
	"github.com/phlin/go-agent/internal/application/ports"
	"github.com/phlin/go-agent/internal/config"
)

type vectorGraph struct {
	memory ports.VectorMemoryStore
	meme   ports.VectorMemeStore
}

// buildVectorGraph owns optional vector dependencies and their lifecycle
// registration. The business graph can depend on the returned ports without
// knowing whether vector search is configured or available.
func buildVectorGraph(ctx context.Context, cfg config.Config, factory *modeladapter.Factory, stores *storeBundle) vectorGraph {
	graph := vectorGraph{}
	dim := cfg.Storage.Postgres.VectorDim
	if dim <= 0 {
		return graph
	}
	embedder, err := factory.EmbeddingModel(ctx)
	if err != nil {
		slog.Warn("app: embedding model unavailable, skipping vector store init", "err", err)
		return graph
	}
	probe, err := embedder.EmbedStrings(ctx, []string{"embedding health check"})
	if err != nil {
		slog.Warn("app: embedding probe failed, skipping vector store init", "err", err)
		return graph
	}
	if len(probe) != 1 || len(probe[0]) != dim {
		slog.Warn("app: embedding dimension mismatch, skipping vector store init",
			"expected_dim", dim, "actual_dim", embeddingDimension(probe))
		return graph
	}
	// 向量库与关系库共用 *sql.DB:同一 PG 实例、同一连接池。
	// VectorStore 同时实现 VectorMemoryStore 与 VectorMemeStore。
	vectorStore := postgresstore.NewVectorStore(stores.db, embedder, dim)
	graph.memory = vectorStore
	graph.meme = vectorStore
	return graph
}

func embeddingDimension(vectors [][]float64) int {
	if len(vectors) != 1 {
		return 0
	}
	return len(vectors[0])
}
