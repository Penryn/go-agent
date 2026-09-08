package scene

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/phlin/go-agent/internal/application/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	scenedomain "github.com/phlin/go-agent/internal/domain/scene"
)

type Service struct {
	store ports.GroupSceneStore
}

func New(store ports.GroupSceneStore) *Service { return &Service{store: store} }

func (s *Service) ObserveEvent(ctx context.Context, event conversationdomain.ConversationEvent) error {
	if s == nil || s.store == nil {
		return errors.New("scene: store is not configured")
	}
	if event.GroupID <= 0 || event.EventID == "" {
		return errors.New("scene: group_id and event_id are required")
	}
	current, err := s.store.LoadGroupScene(ctx, event.GroupID)
	if err != nil {
		return err
	}
	if current.LastEventID == event.EventID {
		return nil
	}
	return s.store.SaveGroupScene(ctx, applyEvent(current, event, time.Now()))
}

func applyEvent(current scenedomain.GroupScene, event conversationdomain.ConversationEvent, now time.Time) scenedomain.GroupScene {
	if current.GroupID == 0 {
		current = scenedomain.New(event.GroupID)
	}
	if event.GroupID != 0 {
		current.GroupID = event.GroupID
	}
	if now.IsZero() {
		now = time.Now()
	}
	at := time.Unix(event.TimestampUnix, 0)
	if event.TimestampUnix <= 0 {
		at = now
	}
	if event.Origin == "outbound" || event.UserID == 0 {
		current.LastBotMessageAt = at
	} else {
		current.LastHumanMessageAt = at
		current.ActiveSpeakers = appendUniqueID(current.ActiveSpeakers, event.UserID, 8)
		current.Audience = appendUniqueID(current.Audience, event.UserID, 16)
	}
	if topic := trimTopic(event.Text); topic != "" && event.Origin != "outbound" {
		current.CurrentTopic = topic
	}
	current.ActivityLevel = min01(current.ActivityLevel*0.7 + 0.3)
	if event.MentionedBot || event.NamedBot || event.IsReplyToBot {
		current.SocialTemperature = min01(current.SocialTemperature*0.6 + 0.4)
		current.RecommendedRole = scenedomain.RoleParticipant
	} else if current.RecommendedRole == "" {
		current.RecommendedRole = scenedomain.RoleObserver
	}
	current.LastEventID = event.EventID
	current.Revision++
	current.UpdatedAt = now
	return current
}

func appendUniqueID(values []int64, value int64, limit int) []int64 {
	if value == 0 {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	values = append(values, value)
	if limit > 0 && len(values) > limit {
		values = values[len(values)-limit:]
	}
	return values
}

func trimTopic(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > 80 {
		runes = runes[:80]
	}
	return string(runes)
}

func min01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func (s *Service) Scene(ctx context.Context, groupID int64) (scenedomain.GroupScene, error) {
	if s == nil || s.store == nil {
		return scenedomain.GroupScene{}, errors.New("scene: store is not configured")
	}
	return s.store.LoadGroupScene(ctx, groupID)
}
