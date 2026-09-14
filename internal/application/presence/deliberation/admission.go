package deliberation

import (
	"strings"
	"unicode"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
)

// admissionReason is intentionally small and deterministic. It runs after the
// context snapshot has been assembled but before the expensive model/tool loop.
// The model still owns nuanced social expression when a turn is admitted.
func admissionReason(snapshot conversationdomain.ContextSnapshot) string {
	event := snapshot.Event
	directed := event.MentionedBot || event.NamedBot || event.IsReplyToBot

	// Repeated delivery of the same short inbound message is usually a replay
	// or duplicate platform event. Do not spend a model turn replying twice.
	if !directed && isDuplicateInbound(snapshot) {
		return "duplicate_message"
	}
	return ""
}

func isDuplicateInbound(snapshot conversationdomain.ContextSnapshot) bool {
	current := normalizeAdmissionText(snapshot.Event.Text)
	if current == "" || snapshot.Event.UserID == 0 {
		return false
	}
	for i := len(snapshot.RecentTurns) - 1; i >= 0; i-- {
		turn := snapshot.RecentTurns[i]
		if turn.EventID == snapshot.Event.EventID || (turn.MessageID != "" && turn.MessageID == snapshot.Event.MessageID) {
			continue
		}
		if turn.Origin == "outbound" || turn.UserID != snapshot.Event.UserID {
			return false
		}
		return normalizeAdmissionText(turn.Text) == current
	}
	return false
}

func normalizeAdmissionText(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	var builder strings.Builder
	for _, r := range value {
		if unicode.IsSpace(r) {
			if builder.Len() > 0 {
				builder.WriteByte(' ')
			}
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}
