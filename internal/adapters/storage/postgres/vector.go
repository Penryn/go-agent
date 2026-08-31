package postgresstore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/pgvector/pgvector-go"

	"github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

// VectorStore 基于 pgvector 实现语义向量检索,同一 *sql.DB 上与关系表共用连接池。
// 不依赖 eino-ext:embedder 调用与向量读写都在本包内完成。
type VectorStore struct {
	db       *sql.DB
	embedder embedding.Embedder
}

func NewVectorStore(db *sql.DB, embedder embedding.Embedder, _ int) *VectorStore {
	// 第三个参数 vectorDim 保留在签名里:调用方(bootstrap)从配置传入,
	// 但维度真实约束在 DDL(vector(2048)),插入时不匹配会由 PG 报错(fail-fast)。
	return &VectorStore{db: db, embedder: embedder}
}

// embed 单条文本。
func (s *VectorStore) embed(ctx context.Context, text string) (pgvector.Vector, error) {
	vectors, err := s.embedder.EmbedStrings(ctx, []string{text})
	if err != nil {
		return pgvector.Vector{}, fmt.Errorf("embed text: %w", err)
	}
	if len(vectors) != 1 {
		return pgvector.Vector{}, fmt.Errorf("embed text: expected 1 vector, got %d", len(vectors))
	}
	return toFloat32(vectors[0]), nil
}

func toFloat32(vec []float64) pgvector.Vector {
	out := make([]float32, len(vec))
	for i, v := range vec {
		out[i] = float32(v)
	}
	return pgvector.NewVector(out)
}

// StoreMemory 实现 ports.VectorMemoryStore,以 memory_id 为主键幂等覆盖。
func (s *VectorStore) StoreMemory(ctx context.Context, record memorydomain.MemoryRecord) error {
	content := record.Subject + "\n" + record.Content
	vec, err := s.embed(ctx, content)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO memory_vectors (memory_id, content, embedding)
		VALUES ($1, $2, $3)
		ON CONFLICT (memory_id) DO UPDATE SET content = EXCLUDED.content, embedding = EXCLUDED.embedding
	`, record.MemoryID, content, vec)
	return err
}

// SearchMemories 实现 ports.VectorMemoryStore。
// <=> 是余弦距离(0=相同),相似度 = 1 - distance,低于 threshold 的结果丢弃。
func (s *VectorStore) SearchMemories(ctx context.Context, query string, topK int, threshold float64) ([]memorydomain.MemoryRecord, error) {
	if topK <= 0 {
		topK = 5
	}
	vec, err := s.embed(ctx, query)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT memory_id, content, 1 - (embedding <=> $1) AS similarity
		FROM memory_vectors
		ORDER BY embedding <=> $1
		LIMIT $2
	`, vec, topK)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := []memorydomain.MemoryRecord{}
	for rows.Next() {
		var (
			record     memorydomain.MemoryRecord
			similarity float64
		)
		if err := rows.Scan(&record.MemoryID, &record.Content, &similarity); err != nil {
			return nil, err
		}
		if similarity < threshold {
			continue
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

// IndexMeme 实现 ports.VectorMemeStore,以 meme_id 为主键幂等覆盖。
func (s *VectorStore) IndexMeme(ctx context.Context, memeID string, text string, groupID int64) error {
	vec, err := s.embed(ctx, text)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO meme_vectors (meme_id, group_id, text, embedding)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (meme_id) DO UPDATE SET group_id = EXCLUDED.group_id, text = EXCLUDED.text, embedding = EXCLUDED.embedding
	`, memeID, groupID, text, vec)
	return err
}

// SearchMemes 实现 ports.VectorMemeStore。
// group 过滤下推到 SQL(WHERE group_id),topK 不被其他群的数据稀释。
// Descriptor 字段为零值,由上层 MemeService 回查关系表补全。
func (s *VectorStore) SearchMemes(ctx context.Context, groupID int64, queryText string, topK int, threshold float64) ([]media.MemeSearchResult, error) {
	if topK <= 0 {
		topK = 5
	}
	vec, err := s.embed(ctx, queryText)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT meme_id, 1 - (embedding <=> $1) AS similarity
		FROM meme_vectors
		WHERE group_id = $2
		ORDER BY embedding <=> $1
		LIMIT $3
	`, vec, groupID, topK)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []media.MemeSearchResult{}
	for rows.Next() {
		var (
			result     media.MemeSearchResult
			similarity float64
		)
		if err := rows.Scan(&result.MemeID, &similarity); err != nil {
			return nil, err
		}
		if similarity < threshold {
			continue
		}
		result.Score = similarity
		result.MatchType = "vector"
		results = append(results, result)
	}
	return results, rows.Err()
}

// DeleteMeme 实现 ports.VectorMemeStore。
func (s *VectorStore) DeleteMeme(ctx context.Context, memeID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM meme_vectors WHERE meme_id = $1`, memeID)
	return err
}
