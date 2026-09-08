package relationship

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/phlin/go-agent/internal/application/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	relationshipdomain "github.com/phlin/go-agent/internal/domain/relationship"
)

type Service struct {
	store     ports.RelationshipStore
	personaID string
}

func New(store ports.RelationshipStore, personaID string) *Service {
	return &Service{store: store, personaID: personaID}
}

func (s *Service) ObserveInbound(ctx context.Context, event conversationdomain.ConversationEvent) error {
	if event.Origin == "outbound" || event.UserID == 0 || event.GroupID == 0 {
		return nil
	}
	kind := relationshipdomain.EventMessageReceived
	intensity := 0.5
	if event.MentionedBot || event.NamedBot || event.IsReplyToBot {
		intensity = 1
	}
	return s.Apply(ctx, relationshipdomain.Event{
		EventID:         "relationship:" + event.EventID,
		PersonaID:       s.personaID,
		GroupID:         event.GroupID,
		UserID:          event.UserID,
		Kind:            kind,
		Intensity:       intensity,
		EvidenceEventID: event.EventID,
		CreatedAt:       eventTime(event.TimestampUnix),
	})
}

func (s *Service) RecordReply(ctx context.Context, snapshot conversationdomain.ContextSnapshot, decisionID string) error {
	if snapshot.Event.UserID == 0 || snapshot.Event.GroupID == 0 {
		return nil
	}
	return s.Apply(ctx, relationshipdomain.Event{
		EventID:         "relationship:reply:" + decisionID,
		PersonaID:       s.personaID,
		GroupID:         snapshot.Event.GroupID,
		UserID:          snapshot.Event.UserID,
		Kind:            relationshipdomain.EventDirectReply,
		Intensity:       1,
		EvidenceEventID: snapshot.Event.EventID,
		DecisionID:      decisionID,
		CreatedAt:       time.Now(),
	})
}

func (s *Service) Apply(ctx context.Context, event relationshipdomain.Event) error {
	if s == nil || s.store == nil {
		return errors.New("relationship: store is not configured")
	}
	if event.EventID == "" || event.GroupID == 0 || event.UserID == 0 {
		return errors.New("relationship: event id, persona, group and user are required")
	}
	if event.PersonaID == "" {
		event.PersonaID = s.personaID
	}
	if event.PersonaID == "" {
		return errors.New("relationship: persona is required")
	}
	if !validEventKind(event.Kind) {
		return fmt.Errorf("relationship: unsupported event kind %q", event.Kind)
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	if _, err := s.store.ApplyRelationshipEvent(ctx, event); err != nil {
		return fmt.Errorf("apply relationship event: %w", err)
	}
	return nil
}

func validEventKind(kind relationshipdomain.EventKind) bool {
	switch kind {
	case relationshipdomain.EventMessageReceived,
		relationshipdomain.EventDirectReply,
		relationshipdomain.EventPositiveFeedback,
		relationshipdomain.EventNegativeFeedback,
		relationshipdomain.EventHelpGiven,
		relationshipdomain.EventHelpReceived,
		relationshipdomain.EventUserCorrection,
		relationshipdomain.EventUserTeasing,
		relationshipdomain.EventTeasingRejected,
		relationshipdomain.EventBotOvertalked,
		relationshipdomain.EventConversationKept,
		relationshipdomain.EventConversationDropped:
		return true
	default:
		return false
	}
}

func eventTime(timestamp int64) time.Time {
	if timestamp <= 0 {
		return time.Now()
	}
	return time.Unix(timestamp, 0)
}
