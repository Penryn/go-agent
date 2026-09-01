package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/phlin/go-agent/internal/application/ports"
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

// WithVectorStore 注入语义向量存储，用于异步同步到向量库。
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
	atomicStore ports.AtomicMemoryProjectionStore
	vectorStore ports.VectorMemoryStore // 可为 nil，nil 时跳过向量写入
	typeTTL     map[string]string       // 按类型差异化 TTL，可为 nil
	defaultTTL  string                  // 全局默认 TTL（如 "720h"），空时不设过期
	outbox      interface {
		Enqueue(context.Context, string, string, []byte) error
	}
}

func WithOutbox(runtime interface {
	Enqueue(context.Context, string, string, []byte) error
}) Option {
	return func(s *Service) { s.outbox = runtime }
}

func WithAtomicProjectionStore(store ports.AtomicMemoryProjectionStore) Option {
	return func(s *Service) { s.atomicStore = store }
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
		memoryID = fmt.Sprintf("memory-%d", time.Now().UnixNano())
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
	record.Revision = record.CreatedAt.UnixNano()

	// When the relational adapter can share its transaction with the outbox,
	// commit the fact and projection wake-up together.
	if s.vectorStore != nil && s.outbox != nil && s.atomicStore != nil {
		payload, err := json.Marshal(record)
		if err != nil {
			return memorydomain.MemoryRecord{}, err
		}
		key := projectionKey(record.MemoryID, record.Revision)
		if err := s.atomicStore.UpsertMemoryAndEnqueueVector(ctx, record, ports.OutboxTask{
			ID:             "memory-vector-" + key,
			Kind:           "memory_vector_index",
			IdempotencyKey: key,
			Payload:        payload,
		}); err != nil {
			return memorydomain.MemoryRecord{}, err
		}
		return record, nil
	}

	// Step 1: 写入主存储（失败则整体失败）
	if err := s.store.UpsertMemory(ctx, record); err != nil {
		return memorydomain.MemoryRecord{}, err
	}

	// Step 2: submit vector synchronization through the durable outbox.
	if s.vectorStore != nil {
		if s.outbox != nil {
			payload, marshalErr := json.Marshal(record)
			if marshalErr == nil {
				if enqueueErr := s.outbox.Enqueue(ctx, "memory_vector_index", projectionKey(record.MemoryID, record.Revision), payload); enqueueErr == nil {
					return record, nil
				} else {
					marshalErr = enqueueErr
				}
			}
			slog.Warn("memory: outbox enqueue failed, indexing synchronously", "memory_id", record.MemoryID, "err", marshalErr)
		}
		if err := s.vectorStore.StoreMemory(ctx, record); err != nil {
			slog.Warn("memory: vector sync failed", "memory_id", record.MemoryID, "err", err)
		}
	}

	return record, nil
}

// ProcessVectorIndex executes one durable vector synchronization task.
func (s *Service) ProcessVectorIndex(ctx context.Context, record memorydomain.MemoryRecord) error {
	if s == nil || s.vectorStore == nil {
		return fmt.Errorf("memory: vector store is not configured")
	}
	return s.vectorStore.StoreMemory(ctx, record)
}

func projectionKey(id string, revision int64) string {
	if revision <= 0 {
		return id
	}
	return fmt.Sprintf("%s:%d", id, revision)
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
