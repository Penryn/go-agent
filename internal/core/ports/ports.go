package ports

import (
	"context"
	"time"

	"github.com/cloudwego/eino/components/model"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

type InboundSource interface {
	Receive(ctx context.Context, handler func(context.Context, []byte) error) error
}

type OutboundSender interface {
	Send(ctx context.Context, action replydomain.ActionExecution) (replydomain.ActionReceipt, error)
}

type MemoryQuery struct {
	GroupID int64
	UserID  int64
	Query   string
	TopK    int
	Scope   string
	Types   []string
}

type MemeQuery struct {
	GroupID       int64
	Query         string
	Emotion       string
	Scene         string
	TopK          int
	ExcludeRecent bool
}

type MemoryStore interface {
	ArchiveEvent(ctx context.Context, event conversationdomain.ConversationEvent) error
	RecentEvents(ctx context.Context, groupID int64, limit int) ([]conversationdomain.ConversationEvent, error)
	UpsertMemory(ctx context.Context, record memorydomain.MemoryRecord) error
	QueryMemories(ctx context.Context, query MemoryQuery) ([]memorydomain.MemoryRecord, error)
}

// ThoughtStore persists structured deliberation summaries for evaluation and
// future learning. It deliberately does not expose raw model chain-of-thought.
type ThoughtStore interface {
	SaveThought(ctx context.Context, thought replydomain.ThoughtRecord) error
}

// OutboxTask is the durable envelope for asynchronous work. Payload is
// versioned by Kind and must be safe to replay; IdempotencyKey prevents
// duplicate enqueue on retries.
type OutboxTask struct {
	ID             string
	Kind           string
	IdempotencyKey string
	Payload        []byte
	Status         string
	Attempts       int
	MaxAttempts    int
	AvailableAt    time.Time
	LockedUntil    time.Time
	LockedBy       string
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

const (
	OutboxPending    = "pending"
	OutboxRunning    = "running"
	OutboxRetry      = "retry"
	OutboxCompleted  = "completed"
	OutboxDeadLetter = "dead_letter"
)

// OutboxStore is the persistence seam for replayable background tasks.
type OutboxStore interface {
	EnqueueOutbox(ctx context.Context, task OutboxTask) error
	ClaimOutbox(ctx context.Context, workerID string, now time.Time, lease time.Duration, limit int) ([]OutboxTask, error)
	CompleteOutbox(ctx context.Context, id string) error
	FailOutbox(ctx context.Context, id string, taskErr error, retryAt time.Time) error
}

// LearningStateStore owns the durable cursor for background projectors. It
// is intentionally separate from MemoryStore because a projector needs a
// stable ordered read, not just a rolling conversation window.
type LearningStateStore interface {
	EventsAfter(ctx context.Context, groupID int64, after time.Time, afterEventID string, limit int) ([]conversationdomain.ConversationEvent, error)
	GetLearningWatermark(ctx context.Context, groupID int64, kind string) (memorydomain.LearningWatermark, error)
	SaveLearningWatermark(ctx context.Context, watermark memorydomain.LearningWatermark) error
}

type MemeStore interface {
	UpsertMeme(ctx context.Context, asset mediadomain.MemeAsset, descriptor mediadomain.MemeDescriptor) error
	SearchMemes(ctx context.Context, query MemeQuery) ([]mediadomain.MemeSearchResult, error)
	GetMeme(ctx context.Context, memeID string) (mediadomain.MemeAsset, mediadomain.MemeDescriptor, error)
	MarkMemeSent(ctx context.Context, memeID string) error
	CountMemesByGroup(ctx context.Context, groupID int64) (int, error)
	DeleteOldestMemes(ctx context.Context, groupID int64, deleteCount int) error
}

type ProfileStore interface {
	GetMemberProfile(ctx context.Context, groupID, userID int64) (profiledomain.MemberProfile, error)
	SaveMemberProfile(ctx context.Context, profile profiledomain.MemberProfile) error
	GetRelationship(ctx context.Context, personaID string, groupID, userID int64) (profiledomain.RelationshipState, error)
	SaveRelationship(ctx context.Context, state profiledomain.RelationshipState) error
}

type RuntimeStateStore interface {
	GetRuntimeState(ctx context.Context, groupID int64) (policydomain.RuntimeState, error)
	SaveRuntimeState(ctx context.Context, state policydomain.RuntimeState) error
	GetPersonaState(ctx context.Context, personaID string, groupID int64) (personadomain.PersonaState, error)
	SavePersonaState(ctx context.Context, state personadomain.PersonaState) error
}

type ChatModelFactory interface {
	MainChatModel(ctx context.Context) (model.BaseChatModel, error)
	VisionChatModel(ctx context.Context) (model.BaseChatModel, error)
}

// VectorMemoryStore 是语义记忆检索的 port。
// 实现方：qdrantstore.Store；未配置时可用 NoopVectorStore 代替。
type VectorMemoryStore interface {
	// StoreMemory 将一条记忆向量化后存入向量库。
	StoreMemory(ctx context.Context, record memorydomain.MemoryRecord) error
	// SearchMemories 执行语义检索，返回相似度 >= threshold 的记录。
	SearchMemories(ctx context.Context, query string, topK int, threshold float64) ([]memorydomain.MemoryRecord, error)
}

// NoopVectorStore 是 VectorMemoryStore 的空实现，用于 Qdrant 未配置时。
type NoopVectorStore struct{}

func (NoopVectorStore) StoreMemory(_ context.Context, _ memorydomain.MemoryRecord) error {
	return nil
}

func (NoopVectorStore) SearchMemories(_ context.Context, _ string, _ int, _ float64) ([]memorydomain.MemoryRecord, error) {
	return nil, nil
}

// VectorMemeStore 是表情包语义向量检索的 port。
// 实现方：qdrantstore.MemeVectorStore；未配置时可用 NoopVectorMemeStore 代替。
// SearchMemes 返回的 MemeSearchResult 中 Descriptor 字段为零值，由上层 MemeService 回查 MySQL 补全。
type VectorMemeStore interface {
	// IndexMeme 将表情包描述文本向量化后存入向量库。
	IndexMeme(ctx context.Context, memeID string, text string, groupID int64) error
	// SearchMemes 执行语义检索，返回相似度 >= threshold 的记录（Descriptor 字段为零值）。
	SearchMemes(ctx context.Context, groupID int64, queryText string, topK int, threshold float64) ([]mediadomain.MemeSearchResult, error)
	// DeleteMeme 从向量库删除指定表情包。
	DeleteMeme(ctx context.Context, memeID string) error
}

// NoopVectorMemeStore 是 VectorMemeStore 的空实现，用于 Qdrant 未配置时。
type NoopVectorMemeStore struct{}

func (NoopVectorMemeStore) IndexMeme(_ context.Context, _ string, _ string, _ int64) error {
	return nil
}

func (NoopVectorMemeStore) SearchMemes(_ context.Context, _ int64, _ string, _ int, _ float64) ([]mediadomain.MemeSearchResult, error) {
	return nil, nil
}

func (NoopVectorMemeStore) DeleteMeme(_ context.Context, _ string) error {
	return nil
}
