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

type OutboundSender interface {
	Send(ctx context.Context, action replydomain.ActionExecution) (replydomain.ActionReceipt, error)
}

// ReadAckingSender 是可选的拟人回执能力：在回复前把触发消息标记已读。
// 实现方：napcat sender（调 mark_msg_as_read）。不支持的平台返回 nil 即可。
type ReadAckingSender interface {
	MarkRead(ctx context.Context, groupID int64, messageID string) error
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

// AtomicMemoryProjectionStore commits the authoritative memory and its durable
// vector projection task in one database transaction.
type AtomicMemoryProjectionStore interface {
	UpsertMemoryAndEnqueueVector(ctx context.Context, record memorydomain.MemoryRecord, task OutboxTask) error
}

// ThoughtStore persists structured deliberation summaries for evaluation and
// future learning. It deliberately does not expose raw model chain-of-thought.
type ThoughtStore interface {
	SaveThought(ctx context.Context, thought replydomain.ThoughtRecord) error
	// RecentThoughts 返回一群最近的思考记录（新到旧），供下轮决策回看。
	RecentThoughts(ctx context.Context, groupID int64, limit int) ([]replydomain.ThoughtRecord, error)
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
	// MarkMemeDud 给表情记一次哑弹（发送后群里持续冷场）。
	MarkMemeDud(ctx context.Context, memeID string) error
	CountMemesByGroup(ctx context.Context, groupID int64) (int, error)
	DeleteOldestMemes(ctx context.Context, groupID int64, deleteCount int) error
}

// AtomicMemeProjectionStore commits a meme fact and its durable vector task
// in one transaction, closing the crash window between the two writes.
type AtomicMemeProjectionStore interface {
	UpsertMemeAndEnqueueVector(ctx context.Context, asset mediadomain.MemeAsset, descriptor mediadomain.MemeDescriptor, task OutboxTask) error
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
// 实现方：postgresstore.VectorStore；未配置时传 nil，调用方自行跳过向量路径。
type VectorMemoryStore interface {
	// StoreMemory 将一条记忆向量化后存入向量库。
	StoreMemory(ctx context.Context, record memorydomain.MemoryRecord) error
	// SearchMemories 执行语义检索，并遵守与词法检索相同的 scope、type、expiry 和 TopK 约束。
	SearchMemories(ctx context.Context, query MemoryQuery, threshold float64) ([]memorydomain.MemoryRecord, error)
}

// VectorMemeStore 是表情包语义向量检索的 port。
// 实现方：postgresstore.VectorStore；未配置时传 nil，降级为关键词搜索。
// SearchMemes 返回的 MemeSearchResult 中 Descriptor 字段为零值，由上层 MemeService 回查关系表补全。
type VectorMemeStore interface {
	// IndexMeme 将表情包描述文本向量化后存入向量库。
	IndexMeme(ctx context.Context, memeID string, text string, groupID int64) error
	// IndexMemeVersioned 携带描述 revision 入库，投影任务用它避免旧版本覆盖新版本。
	IndexMemeVersioned(ctx context.Context, memeID string, text string, groupID int64, revision int64) error
	// SearchMemes 执行语义检索，返回相似度 >= threshold 的记录（Descriptor 字段为零值）。
	SearchMemes(ctx context.Context, groupID int64, queryText string, topK int, threshold float64) ([]mediadomain.MemeSearchResult, error)
}
