package bootstrap

import (
	"context"

	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
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
// 阶段 A:向量存储暂降级为 Noop,阶段 B 切到 pgvector 后恢复。
func buildVectorGraph(ctx context.Context, cfg config.Config, factory *modeladapter.Factory, stores *storeBundle) vectorGraph {
	return vectorGraph{meme: ports.NoopVectorMemeStore{}}
}
