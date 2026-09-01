package context

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	policysvc "github.com/phlin/go-agent/internal/application/policy"
	"github.com/phlin/go-agent/internal/application/ports"
	retrievalsvc "github.com/phlin/go-agent/internal/application/retrieval"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
)

// WorkingMemoryReader supplies the fast, per-group conversation state. It is
// preferred over the durable store for the live tail because it already
// contains the event currently being processed.
type WorkingMemoryReader interface {
	Snapshot(context.Context, int64) (presencedomain.GroupWorkingMemory, error)
}

type Service struct {
	memoryStore   ports.MemoryStore
	profileStore  ports.ProfileStore
	stateStore    ports.RuntimeStateStore
	policy        *policysvc.Service
	persona       personadomain.PersonaConfig
	memoryTopK    int
	workingMemory WorkingMemoryReader
	thoughts      ports.ThoughtStore
	personaFacts  ports.PersonaFactStore
	retriever     *retrievalsvc.Service
}

// WithThoughtStore 启用「回看上次判断」；不注入时快照不带 RecentThoughts。
func (s *Service) WithThoughtStore(store ports.ThoughtStore) { s.thoughts = store }

func (s *Service) WithWorkingMemory(reader WorkingMemoryReader) {
	s.workingMemory = reader
}

func (s *Service) WithPersonaFactStore(store ports.PersonaFactStore) {
	s.personaFacts = store
}

func New(
	memoryStore ports.MemoryStore,
	profileStore ports.ProfileStore,
	stateStore ports.RuntimeStateStore,
	policy *policysvc.Service,
	persona personadomain.PersonaConfig,
	retriever *retrievalsvc.Service,
	memoryTopK int,
) *Service {
	if memoryTopK <= 0 {
		memoryTopK = 4
	}
	return &Service{
		memoryStore:  memoryStore,
		profileStore: profileStore,
		stateStore:   stateStore,
		policy:       policy,
		persona:      persona,
		retriever:    retriever,
		memoryTopK:   memoryTopK,
	}
}

func (s *Service) BuildSnapshot(ctx context.Context, envelope conversationdomain.EventEnvelope, mediaDescriptors []mediadomain.MediaDescriptor) (conversationdomain.ContextSnapshot, error) {
	var recentTurns []conversationdomain.ConversationEvent
	var working presencedomain.GroupWorkingMemory
	if s.workingMemory != nil {
		var wmErr error
		working, wmErr = s.workingMemory.Snapshot(ctx, envelope.Event.GroupID)
		if wmErr != nil {
			return conversationdomain.ContextSnapshot{}, fmt.Errorf("load working memory: %w", wmErr)
		}
		recentTurns = make([]conversationdomain.ConversationEvent, 0, len(working.RecentTail))
		for _, record := range working.RecentTail {
			recentTurns = append(recentTurns, record.Event)
		}
		// Working memory is process-local. Merge the archive to preserve the
		// conversation tail immediately after a restart and while the actor has
		// not yet observed a full window of new events.
		archived, err := s.memoryStore.RecentEvents(ctx, envelope.Event.GroupID, s.policy.AutonomyPolicy().ObserveWindowSize)
		if err != nil {
			return conversationdomain.ContextSnapshot{}, fmt.Errorf("load archived events: %w", err)
		}
		recentTurns = mergeRecentTurns(archived, recentTurns, s.policy.AutonomyPolicy().ObserveWindowSize)
	} else {
		var err error
		recentTurns, err = s.memoryStore.RecentEvents(ctx, envelope.Event.GroupID, s.policy.AutonomyPolicy().ObserveWindowSize)
		if err != nil {
			return conversationdomain.ContextSnapshot{}, fmt.Errorf("load recent events: %w", err)
		}
	}

	relevantMemories, err := s.retriever.SearchMemories(ctx, ports.MemoryQuery{
		GroupID: envelope.Event.GroupID,
		UserID:  envelope.Event.UserID,
		Query:   envelope.Event.Text,
		TopK:    s.memoryTopK,
	})
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

	// persona 状态全局共享（单槽 GroupID=0）：情绪是「我」的状态，
	// 不是「我在这个群」的状态——跨群一致，避免共同好友看到人格分裂。
	personaState, err := s.stateStore.GetPersonaState(ctx, s.persona.ID, 0)
	if err != nil {
		return conversationdomain.ContextSnapshot{}, fmt.Errorf("load persona state: %w", err)
	}
	personaFacts, err := s.currentPersonaFacts(ctx, time.Now())
	if err != nil {
		return conversationdomain.ContextSnapshot{}, fmt.Errorf("load persona facts: %w", err)
	}

	if len(mediaDescriptors) == 0 {
		mediaDescriptors = append([]mediadomain.MediaDescriptor(nil), working.MediaByEvent[envelope.Event.EventID]...)
	}
	// vision 未跑或失败时不再拼「收到一个xx附件」的假描述——
	// 模型看到的媒体描述要么是真实理解，要么没有。
	groupPolicy := s.policy.EffectiveGroupPolicy(envelope.Event.GroupID)
	projection := conversationdomain.ProjectionMetadata{
		Name:     "group_working_memory+archive",
		Version:  working.Version,
		Complete: true,
	}
	if len(recentTurns) > 0 {
		last := recentTurns[len(recentTurns)-1]
		projection.Cursor = conversationdomain.ContextCursor{EventID: last.EventID, TimestampUnix: last.TimestampUnix}
	}
	return conversationdomain.ContextSnapshot{
		SnapshotID:        fmt.Sprintf("snapshot-%d", time.Now().UnixNano()),
		SelfID:            envelope.SelfID,
		Projection:        projection,
		Event:             envelope.Event,
		RecentTurns:       recentTurns,
		RelevantMemories:  relevantMemories,
		RecentThoughts:    s.recentThoughts(ctx, envelope.Event.GroupID),
		MediaDescriptors:  mediaDescriptors,
		ActiveTopic:       working.ActiveTopic,
		OpenLoops:         append([]string(nil), working.OpenLoops...),
		MemberProfile:     ensureMemberProfile(memberProfile, envelope.Event),
		RelationshipState: relationship,
		PersonaState:      personaState,
		PersonaFacts:      append([]personadomain.PersonaFact(nil), personaFacts...),
		GroupPolicy:       groupPolicy,
		RuntimeState:      runtimeState,
		DecisionHints:     buildDecisionHints(envelope.Event),
	}, nil
}

func (s *Service) currentPersonaFacts(ctx context.Context, now time.Time) ([]personadomain.PersonaFact, error) {
	latest := make(map[string]personadomain.PersonaFact)
	for _, seed := range s.persona.InitialFacts {
		key := strings.TrimSpace(seed.Key)
		value := strings.TrimSpace(seed.Value)
		if key == "" || value == "" {
			continue
		}
		effectiveAt, _ := time.Parse(time.RFC3339, strings.TrimSpace(seed.EffectiveAt))
		fact := personadomain.PersonaFact{
			FactID:      "config:" + s.persona.ID + ":" + key,
			PersonaID:   s.persona.ID,
			Key:         key,
			Value:       value,
			Status:      personadomain.PersonaFactVerified,
			SourceKind:  "config",
			Confidence:  1,
			EffectiveAt: effectiveAt,
			RecordedAt:  effectiveAt,
		}
		latest[key+"\x00"+fact.Status] = fact
	}
	if s.personaFacts != nil {
		facts, err := s.personaFacts.CurrentPersonaFacts(ctx, s.persona.ID, now)
		if err != nil {
			return nil, err
		}
		for _, fact := range facts {
			key := fact.Key + "\x00" + fact.Status
			current, ok := latest[key]
			if !ok || fact.EffectiveAt.After(current.EffectiveAt) ||
				(fact.EffectiveAt.Equal(current.EffectiveAt) && fact.RecordedAt.After(current.RecordedAt)) {
				latest[key] = fact
			}
		}
	}
	result := make([]personadomain.PersonaFact, 0, len(latest))
	verifiedByKey := make(map[string]personadomain.PersonaFact)
	for _, fact := range latest {
		if fact.Status == personadomain.PersonaFactVerified {
			verifiedByKey[fact.Key] = fact
		}
	}
	for _, fact := range latest {
		if fact.Status == personadomain.PersonaFactReported {
			if verified, ok := verifiedByKey[fact.Key]; ok && !fact.EffectiveAt.After(verified.EffectiveAt) {
				continue
			}
		}
		result = append(result, fact)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Status == result[j].Status {
			return result[i].Key < result[j].Key
		}
		return result[i].Status == personadomain.PersonaFactVerified
	})
	return result, nil
}

// recentThoughts 取该群最近几轮的判断摘要；失败静默（回看是增强不是依赖）。
func (s *Service) recentThoughts(ctx context.Context, groupID int64) []conversationdomain.ThoughtDigest {
	if s.thoughts == nil {
		return nil
	}
	records, err := s.thoughts.RecentThoughts(ctx, groupID, 5)
	if err != nil {
		slog.WarnContext(ctx, "context: load recent thoughts failed", "group_id", groupID, "err", err)
		return nil
	}
	digests := make([]conversationdomain.ThoughtDigest, 0, len(records))
	for _, record := range records {
		digests = append(digests, conversationdomain.ThoughtDigest{
			Interpretation: record.Interpretation,
			ChosenAction:   record.ChosenAction,
			Outcome:        record.Outcome,
			CreatedAt:      record.CreatedAt,
		})
	}
	return digests
}

func mergeRecentTurns(archived, live []conversationdomain.ConversationEvent, limit int) []conversationdomain.ConversationEvent {
	merged := make([]conversationdomain.ConversationEvent, 0, len(archived)+len(live))
	seen := make(map[string]struct{}, len(archived)+len(live))
	appendUnique := func(events []conversationdomain.ConversationEvent) {
		for _, event := range events {
			key := event.EventID
			if key == "" {
				key = event.MessageID
			}
			if key != "" {
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
			}
			merged = append(merged, event)
		}
	}
	appendUnique(archived)
	appendUnique(live)
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].TimestampUnix == merged[j].TimestampUnix {
			return merged[i].EventID < merged[j].EventID
		}
		return merged[i].TimestampUnix < merged[j].TimestampUnix
	})
	if limit > 0 && len(merged) > limit {
		merged = merged[len(merged)-limit:]
	}
	return merged
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
