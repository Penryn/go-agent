package deliberation

import (
	"testing"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
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
		{intent: "question", action: policydomain.ActionReply},
		{intent: "request_help", action: policydomain.ActionReply},
		{intent: "support", action: policydomain.ActionReply},
		{intent: "gratitude", action: policydomain.ActionReply},
		{intent: "banter", action: policydomain.ActionReply},
		{intent: "observe_only", action: policydomain.ActionSilent},
	}
	for _, tc := range cases {
		decision := decisionFor(conversationdomain.EventEnvelope{TraceID: "trace"}, presencedomain.ThoughtCandidate{Intent: tc.intent})
		if decision.Action != tc.action {
			t.Fatalf("intent %q mapped to %q, want %q", tc.intent, decision.Action, tc.action)
		}
	}
}

func TestResolveActionUsesAllowedPlannerProposal(t *testing.T) {
	cases := []struct {
		name     string
		intent   string
		proposed policydomain.DecisionAction
		want     policydomain.DecisionAction
	}{
		{name: "reply can choose meme", intent: "answer", proposed: policydomain.ActionMemeOnly, want: policydomain.ActionMemeOnly},
		{name: "image reaction can choose text", intent: "react", proposed: policydomain.ActionReply, want: policydomain.ActionReply},
		{name: "planner can stay silent", intent: "answer", proposed: policydomain.ActionSilent, want: policydomain.ActionSilent},
		{name: "reply cannot recall", intent: "answer", proposed: policydomain.ActionRecall, want: policydomain.ActionReply},
		{name: "poke can answer in text", intent: "poke_reply", proposed: policydomain.ActionReply, want: policydomain.ActionReply},
		{name: "poke can poke back", intent: "poke_reply", proposed: policydomain.ActionPokeBack, want: policydomain.ActionPokeBack},
		{name: "meme intent cannot text", intent: "send_meme", proposed: policydomain.ActionReply, want: policydomain.ActionMemeOnly},
		{name: "unknown intent stays silent", intent: "", proposed: policydomain.ActionReply, want: policydomain.ActionSilent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveAction(tc.intent, []policydomain.DecisionAction{tc.proposed}); got != tc.want {
				t.Fatalf("resolveAction() = %q, want %q", got, tc.want)
			}
		})
	}
}
