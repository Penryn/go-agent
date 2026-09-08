package profile

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/phlin/go-agent/internal/application/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
)

type Service struct {
	store ports.ProfileStore
}

func New(store ports.ProfileStore) *Service {
	return &Service{store: store}
}

// ObserveEvent 只维护成员画像统计；关系变化由 relationship.Service 根据事件投影。
func (s *Service) ObserveEvent(ctx context.Context, event conversationdomain.ConversationEvent) error {
	profile, err := s.store.GetMemberProfile(ctx, event.GroupID, event.UserID)
	if err != nil {
		return err
	}

	profile.Stats.GroupID = event.GroupID
	profile.Stats.UserID = event.UserID
	if event.Sender.QQNickname != "" {
		profile.Stats.QQNickname = event.Sender.QQNickname
	}
	if event.Sender.GroupCard != "" {
		profile.Stats.GroupCard = event.Sender.GroupCard
	}
	if event.Sender.DisplayName != "" {
		profile.Stats.Nickname = event.Sender.DisplayName
	} else if profile.Stats.GroupCard != "" {
		profile.Stats.Nickname = profile.Stats.GroupCard
	} else if profile.Stats.QQNickname != "" {
		profile.Stats.Nickname = profile.Stats.QQNickname
	}
	profile.Stats.MessageCount++
	profile.Stats.LastSpokeAt = time.Unix(event.TimestampUnix, 0)
	if profile.Stats.LastSpokeAt.IsZero() {
		profile.Stats.LastSpokeAt = time.Now()
	}
	profile.Stats.ActiveScore = min(profile.Stats.ActiveScore+0.1, 1)
	profile.CommonPhrases = appendIfMissing(profile.CommonPhrases, normalizePhrase(event.Text), 5)

	if err := s.store.SaveMemberProfile(ctx, profile); err != nil {
		return err
	}

	return nil
}

func appendIfMissing(items []string, value string, max int) []string {
	if value == "" || slices.Contains(items, value) {
		return items
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
