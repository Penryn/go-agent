package memory

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log/slog"
	"time"

	"github.com/phlin/go-agent/internal/core/ports"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

type WriteIntent struct {
	// MemoryID 可选；非空时 MarkIntent 直接使用该值作为记录主键，实现幂等写入（相同 ID 的写入会覆盖而非新增）。
	// 空时自动生成基于时间戳的唯一 ID（原有行为）。
	MemoryID      string
	Scope         string
	MemoryType    string
	Subject       string
	Content       string
	SourceEventID string
	Importance    float64
	Confidence    float64
}

// Option 是 Service 的函数式配置项。
type Option func(*Service)

// WithVectorStore 注入语义向量存储，用于在写入 MySQL 后异步同步到 Qdrant。
func WithVectorStore(v ports.VectorMemoryStore) Option {
	return func(s *Service) { s.vectorStore = v }
}

// WithTypeTTL 注入差异化 TTL 配置（map[type]duration字符串），MarkIntent 写入时自动按类型赋 ExpiresAt。
// 例：map[string]string{"group_slang": "720h", "topic_keyword": "168h"}
func WithTypeTTL(ttl map[string]string, defaultTTL string) Option {
	return func(s *Service) {
		s.typeTTL = ttl
		s.defaultTTL = defaultTTL
	}
}

type Service struct {
	store       ports.MemoryStore
	vectorStore ports.VectorMemoryStore // 可为 nil，nil 时跳过向量写入
	typeTTL     map[string]string       // 按类型差异化 TTL，可为 nil
	defaultTTL  string                  // 全局默认 TTL（如 "720h"），空时不设过期
}

// New 创建 Service。vectorStore 通过 WithVectorStore Option 可选注入。
func New(store ports.MemoryStore, opts ...Option) *Service {
	svc := &Service{store: store}
	for _, o := range opts {
		o(svc)
	}
	return svc
}

func (s *Service) MarkIntent(ctx context.Context, intent WriteIntent) (memorydomain.MemoryRecord, error) {
	memoryID := intent.MemoryID
	if memoryID == "" {
		memoryID = fmt.Sprintf("mem-%s-%d-%04x", intent.MemoryType, time.Now().UnixNano(), cryptoRand16())
	}
	record := memorydomain.MemoryRecord{
		MemoryID:      memoryID,
		Scope:         intent.Scope,
		Type:          intent.MemoryType,
		Subject:       intent.Subject,
		Content:       intent.Content,
		SourceEventID: intent.SourceEventID,
		Confidence:    intent.Confidence,
		Importance:    intent.Importance,
		CreatedAt:     time.Now(),
		ExpiresAt:     s.resolveExpiresAt(intent.MemoryType),
	}

	// Step 1: 写入 MySQL（主存储，失败则整体失败）
	if err := s.store.UpsertMemory(ctx, record); err != nil {
		return memorydomain.MemoryRecord{}, err
	}

	// Step 2: 异步写入 Qdrant（副存储，失败只打日志，不影响主流程）
	if s.vectorStore != nil {
		vs := s.vectorStore
		r := record
		safeGo("qdrant_store_memory", func() {
			storeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := vs.StoreMemory(storeCtx, r); err != nil {
				slog.Warn("memory: async qdrant sync failed",
					"memory_id", r.MemoryID,
					"err", err,
				)
			}
		})
	}

	return record, nil
}

func (s *Service) Query(ctx context.Context, query ports.MemoryQuery) ([]memorydomain.MemoryRecord, error) {
	return s.store.QueryMemories(ctx, query)
}

// resolveExpiresAt 根据记忆类型查表返回对应的过期时间指针；未配置或解析失败时返回 nil（永不过期）。
func (s *Service) resolveExpiresAt(memType string) *time.Time {
	ttlStr := ""
	if s.typeTTL != nil {
		ttlStr = s.typeTTL[memType]
	}
	if ttlStr == "" {
		ttlStr = s.defaultTTL
	}
	if ttlStr == "" {
		return nil
	}
	d, err := time.ParseDuration(ttlStr)
	if err != nil || d <= 0 {
		return nil
	}
	t := time.Now().Add(d)
	return &t
}

// safeGo 在独立 goroutine 中执行 fn，带 panic recover 保护。
func safeGo(label string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("memory: async goroutine panic", "label", label, "panic", r)
			}
		}()
		fn()
	}()
}

// cryptoRand16 returns a cryptographically random uint16 value for use in
// generated IDs. On the very unlikely read failure it returns 0.
func cryptoRand16() uint16 {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return binary.LittleEndian.Uint16(b[:])
}
