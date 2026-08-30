package deliberation

import (
	"testing"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
)

func TestDecisionForCandidateIntent(t *testing.T) {
	cases := []struct {
		intent string
		action policydomain.DecisionAction
	}{
		{intent: "answer", action: policydomain.ActionReply},
		{intent: "acknowledge", action: policydomain.ActionReply},
		{intent: "react", action: policydomain.ActionReact},
		{intent: "send_meme", action: policydomain.ActionMemeOnly},
		{intent: "follow_up", action: policydomain.ActionReply},
		{intent: "observe_only", action: policydomain.ActionSilent},
	}
	for _, tc := range cases {
		decision := decisionFor(conversationdomain.EventEnvelope{TraceID: "trace"}, humandomain.ThoughtCandidate{Intent: tc.intent})
		if decision.Action != tc.action {
			t.Fatalf("intent %q mapped to %q, want %q", tc.intent, decision.Action, tc.action)
		}
	}
}
