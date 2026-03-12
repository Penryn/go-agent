package profile

import (
	"context"
	"strings"
	"time"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
)

type Service struct {
	store ports.ProfileStore
}

func New(store ports.ProfileStore) *Service {
	return &Service{store: store}
}

func (s *Service) ObserveEvent(ctx context.Context, event conversationdomain.ConversationEvent) error {
	profile, err := s.store.GetMemberProfile(ctx, event.GroupID, event.UserID)
	if err != nil {
		return err
	}

	profile.Stats.GroupID = event.GroupID
	profile.Stats.UserID = event.UserID
	profile.Stats.MessageCount++
	profile.Stats.LastSpokeAt = time.Unix(event.TimestampUnix, 0)
	if profile.Stats.LastSpokeAt.IsZero() {
		profile.Stats.LastSpokeAt = time.Now()
	}
	profile.Stats.ActiveScore = min(profile.Stats.ActiveScore+0.1, 1)
	profile.CommonPhrases = appendIfMissing(profile.CommonPhrases, normalizePhrase(event.Text), 5)

	return s.store.SaveMemberProfile(ctx, profile)
}

func (s *Service) Query(ctx context.Context, groupID, userID int64) (profiledomain.MemberProfile, error) {
	return s.store.GetMemberProfile(ctx, groupID, userID)
}

func appendIfMissing(items []string, value string, max int) []string {
	if value == "" {
		return items
	}
	for _, item := range items {
		if item == value {
			return items
		}
	}
	items = append(items, value)
	if max > 0 && len(items) > max {
		items = items[len(items)-max:]
	}
	return items
}

func normalizePhrase(text string) string {
	text = strings.TrimSpace(text)
	if len([]rune(text)) > 18 {
		return ""
	}
	return text
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
