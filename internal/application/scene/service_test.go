package scene

import (
	"testing"
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	scenedomain "github.com/phlin/go-agent/internal/domain/scene"
)

func TestApplyEventPromotesDirectAddressToParticipant(t *testing.T) {
	scene := applyEvent(scenedomain.New(1), conversationdomain.ConversationEvent{
		EventID: "event-1", GroupID: 1, UserID: 2, Text: "在吗", MentionedBot: true, TimestampUnix: 100,
	}, time.Unix(100, 0))
	if scene.RecommendedRole != scenedomain.RoleParticipant || scene.CurrentTopic != "在吗" || scene.ActivityLevel == 0 || len(scene.ActiveSpeakers) != 1 {
		t.Fatalf("unexpected group scene: %+v", scene)
	}
}
