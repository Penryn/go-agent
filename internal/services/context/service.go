package context

import (
	"context"
	"fmt"
	"time"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
	policysvc "github.com/phlin/go-agent/internal/services/policy"
)

type Service struct {
	memoryStore  ports.MemoryStore
	profileStore ports.ProfileStore
	stateStore   ports.RuntimeStateStore
	policy       *policysvc.Service
	persona      personadomain.PersonaConfig
}

func New(memoryStore ports.MemoryStore, profileStore ports.ProfileStore, stateStore ports.RuntimeStateStore, policy *policysvc.Service, persona personadomain.PersonaConfig) *Service {
	return &Service{
		memoryStore:  memoryStore,
		profileStore: profileStore,
		stateStore:   stateStore,
		policy:       policy,
		persona:      persona,
	}
}

func (s *Service) BuildSnapshot(ctx context.Context, envelope conversationdomain.EventEnvelope, mediaDescriptors []mediadomain.MediaDescriptor) (conversationdomain.ContextSnapshot, error) {
	recentTurns, err := s.memoryStore.RecentEvents(ctx, envelope.Event.GroupID, s.policy.AutonomyPolicy().ObserveWindowSize)
	if err != nil {
		return conversationdomain.ContextSnapshot{}, fmt.Errorf("load recent events: %w", err)
	}

	relevantMemories, err := s.memoryStore.QueryMemories(ctx, ports.MemoryQuery{
		GroupID: envelope.Event.GroupID,
		UserID:  envelope.Event.UserID,
		Query:   envelope.Event.Text,
		TopK:    4,
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
				"像群友，不像客服",
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
