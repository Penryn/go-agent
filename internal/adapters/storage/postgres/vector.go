package postgresstore

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/pgvector/pgvector-go"

	"github.com/phlin/go-agent/internal/application/ports"
	"github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	searchcore "github.com/phlin/go-agent/internal/search"
)

// pgVectorMaxDim 是 halfvec HNSW 的维度上限(schema/schema.sql 的 halfvec(2048))。
const pgVectorMaxDim = 4000

// VectorStore 基于 pgvector 实现语义向量检索,同一 *sql.DB 上与关系表共用连接池。
// 不依赖 eino-ext:embedder 调用与向量读写都在本包内完成。
type VectorStore struct {
	db       *sql.DB
	embedder embedding.Embedder
	maxDim   int
}

func NewVectorStore(db *sql.DB, embedder embedding.Embedder, vectorDim int) *VectorStore {
	if vectorDim <= 0 || vectorDim > pgVectorMaxDim {
		vectorDim = 2048
	}
	return &VectorStore{db: db, embedder: embedder, maxDim: vectorDim}
}

// embed 单条文本。
func (s *VectorStore) embed(ctx context.Context, text string) (pgvector.HalfVector, error) {
	vectors, err := s.embedder.EmbedStrings(ctx, []string{text})
	if err != nil {
		return pgvector.HalfVector{}, fmt.Errorf("embed text: %w", err)
	}
	if len(vectors) != 1 {
		return pgvector.HalfVector{}, fmt.Errorf("embed text: expected 1 vector, got %d", len(vectors))
	}
	if len(vectors[0]) != s.maxDim {
		return pgvector.HalfVector{}, fmt.Errorf("embed text: dimension mismatch, got %d want %d", len(vectors[0]), s.maxDim)
	}
	return pgvector.NewHalfVector(toFloat32(vectors[0])), nil
}

func toFloat32(vec []float64) []float32 {
	out := make([]float32, len(vec))
	for i, v := range vec {
		out[i] = float32(v)
	}
	return out
}

// StoreMemory 实现 ports.VectorMemoryStore,以 memory_id 为主键幂等覆盖。
func (s *VectorStore) StoreMemory(ctx context.Context, record memorydomain.MemoryRecord) error {
	if record.Revision <= 0 {
		_ = s.db.QueryRowContext(ctx, `SELECT revision FROM memories WHERE memory_id = $1`, record.MemoryID).Scan(&record.Revision)
	}
	content := record.Subject + "\n" + record.Content
	vec, err := s.embed(ctx, content)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO memory_vectors (memory_id, content, embedding, source_revision)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (memory_id) DO UPDATE SET content = EXCLUDED.content, embedding = EXCLUDED.embedding, source_revision = EXCLUDED.source_revision
		WHERE memory_vectors.source_revision <= EXCLUDED.source_revision
	`, record.MemoryID, content, vec, record.Revision)
	return err
}

// SearchMemories 实现 ports.VectorMemoryStore。
// <=> 是余弦距离(0=相同),相似度 = 1 - distance。scope、type、expiry 和
// threshold 都在 SQL 中过滤，避免语义轨绕过词法轨的查询约束。
func (s *VectorStore) SearchMemories(ctx context.Context, query ports.MemoryQuery, threshold float64) ([]memorydomain.MemoryRecord, error) {
	if query.TopK <= 0 {
		query.TopK = 5
	}
	if query.Scope != "" && !searchcore.MemoryScopeVisible(query.Scope, query.GroupID, query.UserID) {
		return []memorydomain.MemoryRecord{}, nil
	}
	vec, err := s.embed(ctx, strings.TrimSpace(query.Query))
	if err != nil {
		return nil, err
	}

	statement := `
		SELECT m.memory_id, m.scope, m.type, m.subject, m.content, m.source_event_id, m.descriptor_ref,
		       m.confidence, m.importance, m.revision, m.created_at, m.expires_at,
		       1 - (v.embedding <=> $1) AS similarity
		FROM memory_vectors v
		JOIN memories m ON m.memory_id = v.memory_id
		WHERE (m.expires_at IS NULL OR m.expires_at > NOW())`
	args := []any{vec}
	addArg := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	if query.Scope != "" {
		statement += " AND m.scope = " + addArg(query.Scope)
	} else if query.GroupID != 0 {
		groupScope := addArg(fmt.Sprintf("group:%d", query.GroupID))
		userScope := addArg(fmt.Sprintf("group:%d:user:%d", query.GroupID, query.UserID))
		statement += " AND (m.scope = 'global' OR m.scope = " + groupScope + " OR m.scope = " + userScope + ")"
	} else {
		statement += " AND m.scope = 'global'"
	}
	if len(query.Types) > 0 {
		placeholders := make([]string, 0, len(query.Types))
		for _, memoryType := range query.Types {
			placeholders = append(placeholders, addArg(memoryType))
		}
		statement += " AND m.type IN (" + strings.Join(placeholders, ",") + ")"
	}
	statement += " AND 1 - (v.embedding <=> $1) >= " + addArg(threshold)
	statement += " ORDER BY v.embedding <=> $1 LIMIT " + addArg(query.TopK)

	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := []memorydomain.MemoryRecord{}
	for rows.Next() {
		var (
			record     memorydomain.MemoryRecord
			expiresAt  sql.NullTime
			similarity float64
		)
		if err := rows.Scan(&record.MemoryID, &record.Scope, &record.Type, &record.Subject, &record.Content, &record.SourceEventID, &record.DescriptorRef,
			&record.Confidence, &record.Importance, &record.Revision, &record.CreatedAt, &expiresAt, &similarity); err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			record.ExpiresAt = &expiresAt.Time
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

// IndexMeme 实现 ports.VectorMemeStore,以 meme_id 为主键幂等覆盖。
func (s *VectorStore) IndexMeme(ctx context.Context, memeID string, text string, groupID int64) error {
	return s.IndexMemeVersioned(ctx, memeID, text, groupID, 0)
}

func (s *VectorStore) IndexMemeVersioned(ctx context.Context, memeID string, text string, groupID int64, revision int64) error {
	vec, err := s.embed(ctx, text)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO meme_vectors (meme_id, group_id, text, embedding, source_revision)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (meme_id) DO UPDATE SET group_id = EXCLUDED.group_id, text = EXCLUDED.text, embedding = EXCLUDED.embedding, source_revision = EXCLUDED.source_revision
		WHERE meme_vectors.source_revision <= EXCLUDED.source_revision OR EXCLUDED.source_revision = 0
	`, memeID, groupID, text, vec, revision)
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
