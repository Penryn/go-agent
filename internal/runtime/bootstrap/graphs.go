package bootstrap

import (
	"context"
	"log/slog"

	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
	postgresstore "github.com/phlin/go-agent/internal/adapters/storage/postgres"
	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/core/ports"
)

type vectorGraph struct {
	memory ports.VectorMemoryStore
	meme   ports.VectorMemeStore
}

// buildVectorGraph owns optional vector dependencies and their lifecycle
// registration. The business graph can depend on the returned ports without
// knowing whether vector search is configured or available.
func buildVectorGraph(ctx context.Context, cfg config.Config, factory *modeladapter.Factory, stores *storeBundle) vectorGraph {
	graph := vectorGraph{meme: ports.NoopVectorMemeStore{}}
	if cfg.Storage.Postgres.VectorDim <= 0 {
		return graph
	}
	embedder, err := factory.EmbeddingModel(ctx)
	if err != nil {
		slog.Warn("app: embedding model unavailable, skipping vector store init", "err", err)
		return graph
	}
	// 向量库与关系库共用 *sql.DB:同一 PG 实例、同一连接池。
	// VectorStore 同时实现 VectorMemoryStore 与 VectorMemeStore。
	vectorStore := postgresstore.NewVectorStore(stores.db, embedder, cfg.Storage.Postgres.VectorDim)
	graph.memory = vectorStore
	graph.meme = vectorStore
	return graph
}
