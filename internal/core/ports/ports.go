package ports

import (
	"context"

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

type MemeStore interface {
	UpsertMeme(ctx context.Context, asset mediadomain.MemeAsset, descriptor mediadomain.MemeDescriptor) error
	SearchMemes(ctx context.Context, query MemeQuery) ([]mediadomain.MemeSearchResult, error)
	GetMeme(ctx context.Context, memeID string) (mediadomain.MemeAsset, mediadomain.MemeDescriptor, error)
	MarkMemeSent(ctx context.Context, memeID string) error
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
	GateChatModel(ctx context.Context) (model.BaseChatModel, error)
	VisionChatModel(ctx context.Context) (model.BaseChatModel, error)
}

type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

type WebSearcher interface {
	Search(ctx context.Context, query string, topK int, freshness string) ([]SearchResult, error)
}
