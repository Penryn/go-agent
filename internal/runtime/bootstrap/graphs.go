package bootstrap

import (
	"context"
	"log/slog"

	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
	qdrantstore "github.com/phlin/go-agent/internal/adapters/storage/qdrant"
	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/core/ports"
)

type vectorGraph struct {
	memory *qdrantstore.Store
	meme   ports.VectorMemeStore
}

// buildVectorGraph owns optional vector dependencies and their lifecycle
// registration. The business graph can depend on the returned ports without
// knowing whether Qdrant is configured or available.
func buildVectorGraph(ctx context.Context, cfg config.Config, factory *modeladapter.Factory, stores *storeBundle) vectorGraph {
	graph := vectorGraph{meme: ports.NoopVectorMemeStore{}}
	if cfg.Storage.Qdrant.VectorDim <= 0 {
		return graph
	}
	embedder, err := factory.EmbeddingModel(ctx)
	if err != nil {
		slog.Warn("app: embedding model unavailable, skipping qdrant init", "err", err)
		return graph
	}
	topK := cfg.Memory.SemanticTopK
	if topK <= 0 {
		topK = 6
	}
	qs, err := qdrantstore.New(ctx, cfg.Storage.Qdrant, embedder, cfg.Storage.Qdrant.VectorDim, topK)
	if err != nil {
		slog.Warn("app: qdrant init failed, vector search disabled", "err", err)
		return graph
	}
	graph.memory = qs
	stores.closeFn = append(stores.closeFn, qs.Close)
	stores.probeFn = append(stores.probeFn, qs.Ping)

	if cfg.Storage.Qdrant.MemeCollection == "" {
		return graph
	}
	memeTopK := cfg.Meme.SemanticTopK
	if memeTopK <= 0 {
		memeTopK = 5
	}
	mvs, err := qdrantstore.NewMemeVectorStore(ctx, qs.Client(), cfg.Storage.Qdrant.MemeCollection, embedder, cfg.Storage.Qdrant.VectorDim, memeTopK)
	if err != nil {
		slog.Warn("app: meme vector store init failed, meme semantic search disabled", "err", err)
		return graph
	}
	graph.meme = mvs
	return graph
}
