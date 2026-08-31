package profile

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
)

type Service struct {
	store     ports.ProfileStore
	personaID string
}

func New(store ports.ProfileStore, personaID string) *Service {
	return &Service{store: store, personaID: personaID}
}

// ObserveEvent 更新成员统计，并被动累积熟悉度（上限 0.5）及记录 LastInteractAt。
// 用户首次发言时同步初始化关系（affinity 起点 0.25）。
func (s *Service) ObserveEvent(ctx context.Context, event conversationdomain.ConversationEvent) error {
	profile, err := s.store.GetMemberProfile(ctx, event.GroupID, event.UserID)
	if err != nil {
		return err
	}

	profile.Stats.GroupID = event.GroupID
	profile.Stats.UserID = event.UserID
	firstMessage := profile.Stats.MessageCount == 0
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

	if s.personaID != "" {
		rel, err := s.store.GetRelationship(ctx, s.personaID, event.GroupID, event.UserID)
		if err != nil {
			slog.Warn("profile: load relationship failed", "group_id", event.GroupID, "user_id", event.UserID, "err", err)
			return nil
		}
		rel.PersonaID = s.personaID
		rel.GroupID = event.GroupID
		rel.UserID = event.UserID
		rel.LastInteractAt = time.Now()
		// 首条消息初始化好感度：此后 affinity 的增减只能来自
		// update_affinity 工具的主观判断，被动观察不再碰它。
		if firstMessage {
			rel.Affinity = 0.25
		}
		// 熟悉度记录互动证据，而不是简单把每条消息当成同等质量的关系增长。
		if increment := familiarityEvidence(event); increment > 0 && rel.Familiarity < 0.5 {
			rel.Familiarity = min(rel.Familiarity+increment, 0.5)
		}
		if err := s.store.SaveRelationship(ctx, rel); err != nil {
			slog.Warn("profile: save relationship failed", "group_id", event.GroupID, "user_id", event.UserID, "err", err)
		}
	}

	return nil
}

func familiarityEvidence(event conversationdomain.ConversationEvent) float64 {
	text := strings.TrimSpace(event.Text)
	if text == "" && len(event.Attachments) == 0 {
		return 0
	}
	increment := 0.004
	if len([]rune(text)) >= 4 {
		increment += 0.004
	}
	if event.MentionedBot || event.NamedBot || event.IsReplyToBot {
		increment += 0.006
	}
	if len(event.Attachments) > 0 {
		increment += 0.002
	}
	return increment
}

func (s *Service) Query(ctx context.Context, groupID, userID int64) (profiledomain.MemberProfile, error) {
	return s.store.GetMemberProfile(ctx, groupID, userID)
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
