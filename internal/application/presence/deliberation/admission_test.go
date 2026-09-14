package deliberation

import (
	"testing"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	scenedomain "github.com/phlin/go-agent/internal/domain/scene"
)

func TestAdmissionReasonBlocksDuplicateInboundMessage(t *testing.T) {
	snapshot := conversationdomain.ContextSnapshot{
		Event: conversationdomain.ConversationEvent{EventID: "current", UserID: 7, Text: "  哈哈  "},
		RecentTurns: []conversationdomain.ConversationEvent{
			{EventID: "previous", UserID: 7, Text: "哈哈"},
			{EventID: "current", UserID: 7, Text: "  哈哈  "},
		},
	}
	if got := admissionReason(snapshot); got != "duplicate_message" {
		t.Fatalf("expected duplicate_message, got %q", got)
	}
}

func TestAdmissionReasonDoesNotTreatEarlierMessageAsDuplicateAfterBotReply(t *testing.T) {
	snapshot := conversationdomain.ContextSnapshot{
		Event: conversationdomain.ConversationEvent{EventID: "current", UserID: 7, Text: "哈哈"},
		RecentTurns: []conversationdomain.ConversationEvent{
			{EventID: "earlier", UserID: 7, Text: "哈哈"},
			{EventID: "bot", UserID: 99, Origin: "outbound", Text: "我也是"},
			{EventID: "current", UserID: 7, Text: "哈哈"},
		},
	}
	if got := admissionReason(snapshot); got != "" {
		t.Fatalf("expected a fresh turn after bot reply, got %q", got)
	}
}

func TestAdmissionReasonKeepsDirectMentionAndHealthyPersona(t *testing.T) {
	snapshot := conversationdomain.ContextSnapshot{
		Event:        conversationdomain.ConversationEvent{EventID: "current", UserID: 7, Text: "哈哈", MentionedBot: true},
		PersonaState: personadomain.PersonaState{Mood: string(personadomain.MoodWithdrawn), Energy: string(personadomain.EnergyTired)},
		GroupScene:   scenedomain.GroupScene{RecommendedRole: scenedomain.RoleWithdrawn},
	}
	if got := admissionReason(snapshot); got != "" {
		t.Fatalf("direct mention should remain admissible, got %q", got)
	}
}

func TestAdmissionReasonLeavesPersonaMoodAndSceneToTheModel(t *testing.T) {
	snapshot := conversationdomain.ContextSnapshot{
		Event:        conversationdomain.ConversationEvent{EventID: "current", UserID: 7, Text: "随便聊聊"},
		PersonaState: personadomain.PersonaState{Mood: string(personadomain.MoodWithdrawn)},
		GroupScene:   scenedomain.GroupScene{RecommendedRole: scenedomain.RoleWithdrawn},
	}
	if got := admissionReason(snapshot); got != "" {
		t.Fatalf("mood and scene should remain model context, got %q", got)
	}
}
