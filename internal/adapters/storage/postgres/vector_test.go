package postgresstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/embedding"

	"github.com/phlin/go-agent/internal/core/ports"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

// fakeEmbedder 对相同文本永远返回相同向量,不消耗真实 embedding API。
// 维度必须是 2000:表 DDL 是 vector(2000)(pgvector hnsw 索引上限),维度不匹配 PG 会直接拒绝插入。
func fakeEmbed(ctx context.Context, texts []string) ([][]float64, error) {
	result := make([][]float64, 0, len(texts))
	for _, text := range texts {
		vector := make([]float64, 2000)
		for i, r := range []rune(text) {
			vector[i%4] += float64(r%17) / 17
		}
		result = append(result, vector)
	}
	return result, nil
}

type fakeEmbedder struct{}

func (fakeEmbedder) EmbedStrings(ctx context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	return fakeEmbed(ctx, texts)
}

func TestVectorMemoryRoundtrip(t *testing.T) {
	ctx := context.Background()
	db := setupPostgres(t)
	store := NewVectorStore(db, fakeEmbedder{}, 2000)

	record := memorydomain.MemoryRecord{
		MemoryID: fmt.Sprintf("memory-%d", time.Now().UnixNano()),
		Subject:  "梗",
		Content:  "这个群爱聊旧梗",
	}
	if err := store.StoreMemory(ctx, record); err != nil {
		t.Fatalf("store memory: %v", err)
	}
	// 同 ID 重复写入应覆盖而非报错
	if err := store.StoreMemory(ctx, record); err != nil {
		t.Fatalf("idempotent store: %v", err)
	}

	results, err := store.SearchMemories(ctx, "旧梗", 5, 0.0)
	if err != nil {
		t.Fatalf("search memories: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].MemoryID != record.MemoryID {
		t.Fatalf("unexpected memory id: %s", results[0].MemoryID)
	}

	// threshold=2.0(不可能达到的相似度)应过滤掉全部结果
	none, err := store.SearchMemories(ctx, "旧梗", 5, 2.0)
	if err != nil {
		t.Fatalf("search with high threshold: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("expected 0 results with impossible threshold, got %d", len(none))
	}
}

func TestVectorMemeRoundtrip(t *testing.T) {
	ctx := context.Background()
	db := setupPostgres(t)
	store := NewVectorStore(db, fakeEmbedder{}, 2000)

	memeID := fmt.Sprintf("meme-%d", time.Now().UnixNano())
	if err := store.IndexMeme(ctx, memeID, "离谱文学配图", 1); err != nil {
		t.Fatalf("index meme: %v", err)
	}
	// group 过滤:group 2 查不到 group 1 的表情包
	results, err := store.SearchMemes(ctx, 1, "离谱", 5, 0.0)
	if err != nil {
		t.Fatalf("search memes: %v", err)
	}
	if len(results) != 1 || results[0].MemeID != memeID || results[0].MatchType != "vector" {
		t.Fatalf("unexpected results: %+v", results)
	}
	other, err := store.SearchMemes(ctx, 2, "离谱", 5, 0.0)
	if err != nil {
		t.Fatalf("search memes other group: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("expected 0 results for other group, got %d", len(other))
	}

	if err := store.DeleteMeme(ctx, memeID); err != nil {
		t.Fatalf("delete meme: %v", err)
	}
	after, err := store.SearchMemes(ctx, 1, "离谱", 5, 0.0)
	if err != nil {
		t.Fatalf("search after delete: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("expected 0 results after delete, got %d", len(after))
	}
}

var (
	_ ports.VectorMemoryStore = (*VectorStore)(nil)
	_ ports.VectorMemeStore   = (*VectorStore)(nil)
)
