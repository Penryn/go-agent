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

func parseConfig(cfg config.QdrantConfig) *qdrantclient.Config {
	parsed, err := url.Parse(cfg.URL)
	if err != nil {
		return &qdrantclient.Config{Host: "127.0.0.1", Port: 6334}
	}

	port := 6334
	if parsed.Port() != "" {
		if parsedPort, err := strconv.Atoi(parsed.Port()); err == nil {
			port = parsedPort
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
