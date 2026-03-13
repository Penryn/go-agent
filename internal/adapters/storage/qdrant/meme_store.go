package qdrantstore

import (
	"context"
	"fmt"
	"strconv"

	indexerqdrant "github.com/cloudwego/eino-ext/components/indexer/qdrant"
	retrieverqdrant "github.com/cloudwego/eino-ext/components/retriever/qdrant"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	qdrantclient "github.com/qdrant/go-client/qdrant"

	mediadomain "github.com/phlin/go-agent/internal/domain/media"
)

const metaKeyMemeID = "meme_id"
const metaKeyMemeGroupID = "group_id"

// MemeVectorStore 实现 ports.VectorMemeStore，基于 Qdrant 提供表情包语义向量检索。
// 与 Store 共享同一底层 gRPC client（由构造函数接收），不建立额外连接。
// SearchMemes 返回的 MemeSearchResult 中 Descriptor 为零值，由上层 MemeService 回查 MySQL 补全。
type MemeVectorStore struct {
	client     *qdrantclient.Client
	collection string
	indexer    indexer.Indexer
	retriever  retriever.Retriever
}

// NewMemeVectorStore 构造 MemeVectorStore，接收已有的 qdrantclient.Client 以共享 gRPC 连接。
func NewMemeVectorStore(ctx context.Context, client *qdrantclient.Client, collection string, embedder embedding.Embedder, vectorDim int, topK int) (*MemeVectorStore, error) {
	if err := ensureMemeCollection(ctx, client, collection, vectorDim); err != nil {
		return nil, fmt.Errorf("meme_store: ensure collection %q: %w", collection, err)
	}

	idx, err := indexerqdrant.NewIndexer(ctx, &indexerqdrant.Config{
		Client:     client,
		Collection: collection,
		VectorDim:  vectorDim,
		Distance:   qdrantclient.Distance_Cosine,
		BatchSize:  16,
		Embedding:  embedder,
	})
	if err != nil {
		return nil, fmt.Errorf("meme_store: create indexer: %w", err)
	}

	ret, err := retrieverqdrant.NewRetriever(ctx, &retrieverqdrant.Config{
		Client:     client,
		Collection: collection,
		Embedding:  embedder,
		TopK:       topK,
	})
	if err != nil {
		return nil, fmt.Errorf("meme_store: create retriever: %w", err)
	}

	return &MemeVectorStore{
		collection: collection,
		indexer:    idx,
		retriever:  ret,
	}, nil
}

func ensureMemeCollection(ctx context.Context, client *qdrantclient.Client, collection string, vectorDim int) error {
	exists, err := client.CollectionExists(ctx, collection)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return client.CreateCollection(ctx, &qdrantclient.CreateCollection{
		CollectionName: collection,
		VectorsConfig: qdrantclient.NewVectorsConfig(&qdrantclient.VectorParams{
			Size:     uint64(vectorDim),
			Distance: qdrantclient.Distance_Cosine,
		}),
	})
}

// IndexMeme 实现 ports.VectorMemeStore。
// 将表情包描述文本向量化后以 memeID 为文档 ID 写入 Qdrant（幂等：同 ID 覆盖写）。
func (s *MemeVectorStore) IndexMeme(ctx context.Context, memeID string, text string, groupID int64) error {
	doc := &schema.Document{
		ID:      memeID,
		Content: text,
		MetaData: map[string]any{
			metaKeyMemeID:      memeID,
			metaKeyMemeGroupID: strconv.FormatInt(groupID, 10),
		},
	}
	_, err := s.indexer.Store(ctx, []*schema.Document{doc})
	return err
}

// SearchMemes 实现 ports.VectorMemeStore。
// 返回相似度 >= threshold 的记录，Descriptor 字段为零值，由上层 MemeService 回查 MySQL 补全。
func (s *MemeVectorStore) SearchMemes(ctx context.Context, groupID int64, queryText string, topK int, threshold float64) ([]mediadomain.MemeSearchResult, error) {
	opts := []retriever.Option{}
	if topK > 0 {
		opts = append(opts, retriever.WithTopK(topK))
	}
	docs, err := s.retriever.Retrieve(ctx, queryText, opts...)
	if err != nil {
		return nil, err
	}

	groupIDStr := strconv.FormatInt(groupID, 10)
	results := make([]mediadomain.MemeSearchResult, 0, len(docs))
	for _, doc := range docs {
		if doc.Score() < threshold {
			continue
		}
		// 按 group_id 过滤（仅返回本群表情包）
		if gid, ok := doc.MetaData[metaKeyMemeGroupID].(string); ok && gid != "" && gid != groupIDStr {
			continue
		}
		memeID := doc.ID
		if id, ok := doc.MetaData[metaKeyMemeID].(string); ok && id != "" {
			memeID = id
		}
		results = append(results, mediadomain.MemeSearchResult{
			MemeID:    memeID,
			Score:     doc.Score(),
			MatchType: "vector",
		})
	}
	return results, nil
}

// DeleteMeme 实现 ports.VectorMemeStore。
// 通过 Qdrant client 按 UUID 形式的 Document ID 删除点。
func (s *MemeVectorStore) DeleteMeme(ctx context.Context, memeID string) error {
	_, err := s.client.Delete(ctx, &qdrantclient.DeletePoints{
		CollectionName: s.collection,
		Points:         qdrantclient.NewPointsSelector(qdrantclient.NewID(memeID)),
	})
	return err
}
