package qdrantstore

import (
	"context"
	"net/url"
	"strconv"

	indexerqdrant "github.com/cloudwego/eino-ext/components/indexer/qdrant"
	retrieverqdrant "github.com/cloudwego/eino-ext/components/retriever/qdrant"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	qdrantclient "github.com/qdrant/go-client/qdrant"

	"github.com/phlin/go-agent/internal/config"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

type Store struct {
	client     *qdrantclient.Client
	collection string
	indexer    indexer.Indexer
	retriever  retriever.Retriever
}

func New(ctx context.Context, cfg config.QdrantConfig, embedder embedding.Embedder, vectorDim int, topK int) (*Store, error) {
	client, err := qdrantclient.NewClient(parseConfig(cfg))
	if err != nil {
		return nil, err
	}
	store := &Store{
		client:     client,
		collection: cfg.Collection,
	}
	if err := store.EnsureCollection(ctx, vectorDim); err != nil {
		return nil, err
	}
	idx, err := indexerqdrant.NewIndexer(ctx, &indexerqdrant.Config{
		Client:     client,
		Collection: cfg.Collection,
		VectorDim:  vectorDim,
		Distance:   qdrantclient.Distance_Cosine,
		BatchSize:  16,
		Embedding:  embedder,
	})
	if err != nil {
		return nil, err
	}
	ret, err := retrieverqdrant.NewRetriever(ctx, &retrieverqdrant.Config{
		Client:     client,
		Collection: cfg.Collection,
		Embedding:  embedder,
		TopK:       topK,
	})
	if err != nil {
		return nil, err
	}
	store.indexer = idx
	store.retriever = ret
	return store, nil
}

func (s *Store) Ping(ctx context.Context) error {
	_, err := s.client.HealthCheck(ctx)
	return err
}

func (s *Store) Close() error {
	return s.client.Close()
}

// Client 返回底层 gRPC client，供 MemeVectorStore 共享连接。
func (s *Store) Client() *qdrantclient.Client {
	return s.client
}

func (s *Store) EnsureCollection(ctx context.Context, vectorDim int) error {
	exists, err := s.client.CollectionExists(ctx, s.collection)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return s.client.CreateCollection(ctx, &qdrantclient.CreateCollection{
		CollectionName: s.collection,
		VectorsConfig: qdrantclient.NewVectorsConfig(&qdrantclient.VectorParams{
			Size:     uint64(vectorDim),
			Distance: qdrantclient.Distance_Cosine,
		}),
	})
}

func (s *Store) StoreDocuments(ctx context.Context, docs []*schema.Document) ([]string, error) {
	return s.indexer.Store(ctx, docs)
}

func (s *Store) Retrieve(ctx context.Context, query string, topK int) ([]*schema.Document, error) {
	if topK > 0 {
		return s.retriever.Retrieve(ctx, query, retriever.WithTopK(topK))
	}
	return s.retriever.Retrieve(ctx, query)
}

// metaKeyMemoryID 是存储在 Document.MetaData 里的 memory_id 键名。
const metaKeyMemoryID = "memory_id"

// StoreMemory 实现 ports.VectorMemoryStore。
// Document.ID 使用 MemoryID，保证幂等（Qdrant 同 ID 覆盖写）。
func (s *Store) StoreMemory(ctx context.Context, record memorydomain.MemoryRecord) error {
	doc := &schema.Document{
		ID:      record.MemoryID,
		Content: record.Subject + "\n" + record.Content,
		MetaData: map[string]any{
			metaKeyMemoryID: record.MemoryID,
		},
	}
	_, err := s.indexer.Store(ctx, []*schema.Document{doc})
	return err
}

// SearchMemories 实现 ports.VectorMemoryStore。
// 语义检索后按 threshold 过滤，低于阈值的文档直接丢弃。
func (s *Store) SearchMemories(ctx context.Context, query string, topK int, threshold float64) ([]memorydomain.MemoryRecord, error) {
	docs, err := s.Retrieve(ctx, query, topK)
	if err != nil {
		return nil, err
	}
	records := make([]memorydomain.MemoryRecord, 0, len(docs))
	for _, doc := range docs {
		if doc.Score() < threshold {
			continue
		}
		r := memorydomain.MemoryRecord{
			MemoryID: doc.ID,
			Content:  doc.Content,
		}
		if id, ok := doc.MetaData[metaKeyMemoryID].(string); ok && id != "" {
			r.MemoryID = id
		}
		records = append(records, r)
	}
	return records, nil
}

func parseConfig(cfg config.QdrantConfig) *qdrantclient.Config {
	parsed, err := url.Parse(cfg.URL)
	if err != nil {
		return &qdrantclient.Config{Host: "127.0.0.1", Port: 6334}
	}

	// Qdrant Go client uses gRPC (default port 6334).
	// If the URL specifies the REST port 6333, remap to gRPC port 6334 to avoid
	// the "http2: frame too large" error caused by connecting gRPC to an HTTP/1.1 endpoint.
	port := 6334
	if parsed.Port() != "" {
		if parsedPort, err := strconv.Atoi(parsed.Port()); err == nil {
			if parsedPort == 6333 {
				port = 6334
			} else {
				port = parsedPort
			}
		}
	}
	return &qdrantclient.Config{
		Host:                   parsed.Hostname(),
		Port:                   port,
		APIKey:                 cfg.APIKey,
		UseTLS:                 parsed.Scheme == "https",
		SkipCompatibilityCheck: true,
	}
}
