package context

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
	policysvc "github.com/phlin/go-agent/internal/services/policy"
)

// WorkingMemoryReader supplies the fast, per-group conversation state. It is
// preferred over the durable store for the live tail because it already
// contains the event currently being processed.
type WorkingMemoryReader interface {
	Snapshot(context.Context, int64) (humandomain.GroupWorkingMemory, error)
}

type Service struct {
	memoryStore       ports.MemoryStore
	vectorStore       ports.VectorMemoryStore // 可为 NoopVectorStore，不影响主流程
	profileStore      ports.ProfileStore
	stateStore        ports.RuntimeStateStore
	policy            *policysvc.Service
	persona           personadomain.PersonaConfig
	semanticTopK      int
	semanticThreshold float64
	workingMemory     WorkingMemoryReader
}

func (s *Service) WithWorkingMemory(reader WorkingMemoryReader) {
	s.workingMemory = reader
}

func New(
	memoryStore ports.MemoryStore,
	vectorStore ports.VectorMemoryStore,
	profileStore ports.ProfileStore,
	stateStore ports.RuntimeStateStore,
	policy *policysvc.Service,
	persona personadomain.PersonaConfig,
) *Service {
	return &Service{
		memoryStore:       memoryStore,
		vectorStore:       vectorStore,
		profileStore:      profileStore,
		stateStore:        stateStore,
		policy:            policy,
		persona:           persona,
		semanticTopK:      6,
		semanticThreshold: 0.75,
	}
}

// WithSemanticConfig 允许覆盖语义检索参数（供 app.go 注入 config 值）。
func (s *Service) WithSemanticConfig(topK int, threshold float64) {
	if topK > 0 {
		s.semanticTopK = topK
	}
	if threshold > 0 {
		s.semanticThreshold = threshold
	}
}

func (s *Service) BuildSnapshot(ctx context.Context, envelope conversationdomain.EventEnvelope, mediaDescriptors []mediadomain.MediaDescriptor) (conversationdomain.ContextSnapshot, error) {
	var recentTurns []conversationdomain.ConversationEvent
	if s.workingMemory != nil {
		working, wmErr := s.workingMemory.Snapshot(ctx, envelope.Event.GroupID)
		if wmErr != nil {
			return conversationdomain.ContextSnapshot{}, fmt.Errorf("load working memory: %w", wmErr)
		}
		recentTurns = make([]conversationdomain.ConversationEvent, 0, len(working.RecentTail))
		for _, record := range working.RecentTail {
			recentTurns = append(recentTurns, record.Event)
		}
	} else {
		var err error
		recentTurns, err = s.memoryStore.RecentEvents(ctx, envelope.Event.GroupID, s.policy.AutonomyPolicy().ObserveWindowSize)
		if err != nil {
			return conversationdomain.ContextSnapshot{}, fmt.Errorf("load recent events: %w", err)
		}
	}

	relevantMemories, err := s.queryMemoriesDualTrack(ctx, envelope.Event.GroupID, envelope.Event.UserID, envelope.Event.Text)
	if err != nil {
		return conversationdomain.ContextSnapshot{}, fmt.Errorf("query memories: %w", err)
	}

	memberProfile, err := s.profileStore.GetMemberProfile(ctx, envelope.Event.GroupID, envelope.Event.UserID)
	if err != nil {
		return conversationdomain.ContextSnapshot{}, fmt.Errorf("load member profile: %w", err)
	}

	relationship, err := s.profileStore.GetRelationship(ctx, s.persona.ID, envelope.Event.GroupID, envelope.Event.UserID)
	if err != nil {
		return conversationdomain.ContextSnapshot{}, fmt.Errorf("load relationship state: %w", err)
	}

	runtimeState, err := s.stateStore.GetRuntimeState(ctx, envelope.Event.GroupID)
	if err != nil {
		return conversationdomain.ContextSnapshot{}, fmt.Errorf("load runtime state: %w", err)
	}

	personaState, err := s.stateStore.GetPersonaState(ctx, s.persona.ID, envelope.Event.GroupID)
	if err != nil {
		return conversationdomain.ContextSnapshot{}, fmt.Errorf("load persona state: %w", err)
	}

	if len(mediaDescriptors) == 0 {
		mediaDescriptors = attachmentsAsDescriptors(envelope.Event.Attachments)
	}

	return conversationdomain.ContextSnapshot{
		SnapshotID:        fmt.Sprintf("snapshot-%d", time.Now().UnixNano()),
		Event:             envelope.Event,
		RecentTurns:       recentTurns,
		RelevantMemories:  relevantMemories,
		MediaDescriptors:  mediaDescriptors,
		MemberProfile:     ensureMemberProfile(memberProfile, envelope.Event),
		RelationshipState: relationship,
		PersonaProfile: personadomain.PersonaProfile{
			PersonaID:    s.persona.ID,
			DisplayName:  s.persona.Name,
			StableTraits: []string{s.persona.SpeechStyle, s.persona.Description},
			StyleRules: []string{
				"像真人，不像客服",
				"优先短句",
			},
			AutonomyBias: map[string]float64{
				"prefer_memes":  boolFloat(s.persona.PreferMemes),
				"allow_teasing": boolFloat(s.persona.AllowTeasing),
			},
			OutputRules: []string{
				fmt.Sprintf("最大 %d 字", s.persona.ReplyMaxChars),
				fmt.Sprintf("最大 %d 句", s.persona.ReplyMaxSentences),
			},
		},
		PersonaState:  personaState,
		GroupPolicy:   s.policy.EffectiveGroupPolicy(envelope.Event.GroupID),
		RuntimeState:  runtimeState,
		DecisionHints: buildDecisionHints(envelope.Event),
	}, nil
}

// queryMemoriesDualTrack 并发执行 MySQL 关键词检索 + Qdrant 语义检索，
// 对结果去重合并后返回。Qdrant 失败时降级为纯 MySQL 结果（fail-open）。
func (s *Service) queryMemoriesDualTrack(ctx context.Context, groupID, userID int64, queryText string) ([]memorydomain.MemoryRecord, error) {
	const mysqlTopK = 4

	var (
		mysqlRecords  []memorydomain.MemoryRecord
		qdrantRecords []memorydomain.MemoryRecord
	)

	g, gCtx := errgroup.WithContext(ctx)

	// Track 1: MySQL 关键词检索（主轨，失败则整体失败）
	g.Go(func() error {
		var err error
		mysqlRecords, err = s.memoryStore.QueryMemories(gCtx, ports.MemoryQuery{
			GroupID: groupID,
			UserID:  userID,
			Query:   queryText,
			TopK:    mysqlTopK,
		})
		return err
	})

	// Track 2: Qdrant 语义检索（副轨，失败时降级，不影响主流程）
	g.Go(func() error {
		if queryText == "" {
			return nil
		}
		var err error
		qdrantRecords, err = s.vectorStore.SearchMemories(gCtx, queryText, s.semanticTopK, s.semanticThreshold)
		if err != nil {
			slog.WarnContext(gCtx, "dual-track memory: qdrant semantic search failed, degraded to mysql only",
				"err", err,
				"group_id", groupID,
			)
		}
		return nil // 始终返回 nil，不让 errgroup 取消 MySQL 轨道
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return mergeMemoryResults(mysqlRecords, qdrantRecords, mysqlTopK), nil
}

// mergeMemoryResults 将 MySQL 和 Qdrant 的结果去重合并。
// MySQL 结果作为主数据源（字段完整），Qdrant 补充语义相关但关键词未命中的记录。
func mergeMemoryResults(mysqlRecords, qdrantRecords []memorydomain.MemoryRecord, limit int) []memorydomain.MemoryRecord {
	if len(qdrantRecords) == 0 {
		return mysqlRecords
	}

	seen := make(map[string]struct{}, len(mysqlRecords))
	for _, r := range mysqlRecords {
		seen[r.MemoryID] = struct{}{}
	}

	merged := make([]memorydomain.MemoryRecord, 0, len(mysqlRecords)+len(qdrantRecords))
	merged = append(merged, mysqlRecords...)

	for _, r := range qdrantRecords {
		if _, exists := seen[r.MemoryID]; exists {
			continue
		}
		seen[r.MemoryID] = struct{}{}
		merged = append(merged, r)
	}

	if limit > 0 && len(merged) > limit {
		return merged[:limit]
	}
	return merged
}

func attachmentsAsDescriptors(attachments []mediadomain.MultimodalAttachment) []mediadomain.MediaDescriptor {
	descriptors := make([]mediadomain.MediaDescriptor, 0, len(attachments))
	for _, attachment := range attachments {
		descriptors = append(descriptors, mediadomain.MediaDescriptor{
			AttachmentID: attachment.AttachmentID,
			Kind:         attachment.Kind,
			Summary:      fmt.Sprintf("收到一个%s附件", attachment.Kind),
			Confidence:   0.2,
		})
	}
	return descriptors
}

func ensureMemberProfile(profile profiledomain.MemberProfile, event conversationdomain.ConversationEvent) profiledomain.MemberProfile {
	profile.Stats.GroupID = event.GroupID
	profile.Stats.UserID = event.UserID
	if profile.Stats.LastSpokeAt.IsZero() {
		profile.Stats.LastSpokeAt = time.Unix(event.TimestampUnix, 0)
	}
	return profile
}

func buildDecisionHints(event conversationdomain.ConversationEvent) []string {
	hints := []string{}
	if event.MentionedBot {
		hints = append(hints, "direct_mention")
	}
	if event.NamedBot {
		hints = append(hints, "named_bot")
	}
	if event.IsReplyToBot {
		hints = append(hints, "reply_to_bot")
	}
	if len(event.Attachments) > 0 {
		hints = append(hints, "media_hook")
	}
	return hints
}

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
