package postgresstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/embedding"

	"github.com/phlin/go-agent/internal/application/ports"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

func ptrTime(value time.Time) *time.Time { return &value }

// fakeEmbedder 对相同文本永远返回相同向量,不消耗真实 embedding API。
func fakeEmbed(ctx context.Context, texts []string) ([][]float64, error) {
	result := make([][]float64, 0, len(texts))
	for _, text := range texts {
		vector := make([]float64, 2048)
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

// longFakeEmbedder 模拟 ark embedding-large:输出 2048 维(前 4 维按 hash,其余 0)。
type longFakeEmbedder struct{}

func (longFakeEmbedder) EmbedStrings(ctx context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	result := make([][]float64, 0, len(texts))
	for _, text := range texts {
		vector := make([]float64, 2048)
		for i, r := range []rune(text) {
			vector[i%4] += float64(r%17) / 17
		}
		result = append(result, vector)
	}
	return result, nil
}

type shortFakeEmbedder struct{}

func (shortFakeEmbedder) EmbedStrings(context.Context, []string, ...embedding.Option) ([][]float64, error) {
	return [][]float64{make([]float64, 1024)}, nil
}

func TestEmbedRejectsDimensionMismatch(t *testing.T) {
	store := NewVectorStore(nil, shortFakeEmbedder{}, 2048)
	if _, err := store.embed(context.Background(), "dimension"); err == nil {
		t.Fatal("expected embedding dimension mismatch")
	}
}

func TestVectorMemoryAcceptsConfiguredEmbeddingDimension(t *testing.T) {
	ctx := context.Background()
	db := setupPostgres(t)
	store := NewVectorStore(db, longFakeEmbedder{}, 2048)

	record := memorydomain.MemoryRecord{
		MemoryID: fmt.Sprintf("memory-long-%d", time.Now().UnixNano()),
		Scope:    "global",
		Subject:  "截断",
		Content:  "超维度向量截断后仍可检索",
	}
	if err := NewStore(db).UpsertMemory(ctx, record); err != nil {
		t.Fatalf("store source memory: %v", err)
	}
	if err := store.StoreMemory(ctx, record); err != nil {
		t.Fatalf("store memory: %v", err)
	}
	results, err := store.SearchMemories(ctx, ports.MemoryQuery{Query: "截断", TopK: 5}, 0.0)
	if err != nil {
		t.Fatalf("search memories: %v", err)
	}
	if len(results) != 1 || results[0].MemoryID != record.MemoryID {
		t.Fatalf("unexpected results: %+v", results)
	}
}

func TestVectorMemoryRoundtrip(t *testing.T) {
	ctx := context.Background()
	db := setupPostgres(t)
	store := NewVectorStore(db, fakeEmbedder{}, 2048)

	record := memorydomain.MemoryRecord{
		MemoryID: fmt.Sprintf("memory-%d", time.Now().UnixNano()),
		Scope:    "global",
		Subject:  "梗",
		Content:  "这个群爱聊旧梗",
	}
	if err := NewStore(db).UpsertMemory(ctx, record); err != nil {
		t.Fatalf("store source memory: %v", err)
	}
	if err := store.StoreMemory(ctx, record); err != nil {
		t.Fatalf("store memory: %v", err)
	}
	// 同 ID 重复写入应覆盖而非报错
	if err := store.StoreMemory(ctx, record); err != nil {
		t.Fatalf("idempotent store: %v", err)
	}

	results, err := store.SearchMemories(ctx, ports.MemoryQuery{Query: "旧梗", TopK: 5}, 0.0)
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
	none, err := store.SearchMemories(ctx, ports.MemoryQuery{Query: "旧梗", TopK: 5}, 2.0)
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
	store := NewVectorStore(db, fakeEmbedder{}, 2048)

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
}

func TestVectorMemoryFiltersScopeAndExpiry(t *testing.T) {
	ctx := context.Background()
	db := setupPostgres(t)
	source := NewStore(db)
	store := NewVectorStore(db, fakeEmbedder{}, 2048)
	now := time.Now()
	records := []memorydomain.MemoryRecord{
		{MemoryID: "vector-global", Scope: "global", Type: "global_fact", Subject: "scope", Content: "shared", CreatedAt: now},
		{MemoryID: "vector-group-1", Scope: "group:1", Type: "group_fact", Subject: "scope", Content: "group one", CreatedAt: now},
		{MemoryID: "vector-user-7", Scope: "group:1:user:7", Type: "user_fact", Subject: "scope", Content: "user seven", CreatedAt: now},
		{MemoryID: "vector-group-2", Scope: "group:2", Type: "other_group_fact", Subject: "scope", Content: "group two", CreatedAt: now},
		{MemoryID: "vector-expired", Scope: "group:1", Type: "expired_fact", Subject: "scope", Content: "expired", CreatedAt: now, ExpiresAt: ptrTime(now.Add(-time.Minute))},
	}
	for _, record := range records {
		if err := source.UpsertMemory(ctx, record); err != nil {
			t.Fatal(err)
		}
		if err := store.StoreMemory(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	results, err := store.SearchMemories(ctx, ports.MemoryQuery{Query: "scope", GroupID: 1, UserID: 7, TopK: 10}, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, record := range results {
		seen[record.MemoryID] = true
	}
	if !seen["vector-global"] || !seen["vector-group-1"] || !seen["vector-user-7"] || seen["vector-group-2"] || seen["vector-expired"] {
		t.Fatalf("unexpected scoped vector results: %+v", results)
	}

	explicitScope, err := store.SearchMemories(ctx, ports.MemoryQuery{
		Query: "scope", GroupID: 1, UserID: 7, Scope: "group:1", TopK: 10,
	}, 0)
	if err != nil || len(explicitScope) != 1 || explicitScope[0].MemoryID != "vector-group-1" {
		t.Fatalf("explicit scope was not enforced: results=%+v err=%v", explicitScope, err)
	}

	typed, err := store.SearchMemories(ctx, ports.MemoryQuery{
		Query: "scope", GroupID: 1, UserID: 7, Types: []string{"user_fact"}, TopK: 10,
	}, 0)
	if err != nil || len(typed) != 1 || typed[0].MemoryID != "vector-user-7" {
		t.Fatalf("memory types were not enforced: results=%+v err=%v", typed, err)
	}

	foreignScope, err := store.SearchMemories(ctx, ports.MemoryQuery{
		Query: "scope", GroupID: 1, UserID: 7, Scope: "group:2", TopK: 10,
	}, 0)
	if err != nil || len(foreignScope) != 0 {
		t.Fatalf("foreign scope should fail closed: results=%+v err=%v", foreignScope, err)
	}
}

var (
	_ ports.VectorMemoryStore = (*VectorStore)(nil)
	_ ports.VectorMemeStore   = (*VectorStore)(nil)
)
