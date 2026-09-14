package deliberation

import (
	"context"
	"testing"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

type fixedSnapshotBuilder struct{}

func (fixedSnapshotBuilder) BuildSnapshot(_ context.Context, envelope conversationdomain.EventEnvelope, _ []mediadomain.MediaDescriptor) (conversationdomain.ContextSnapshot, error) {
	return conversationdomain.ContextSnapshot{Event: envelope.Event}, nil
}

type fixedPlanner struct {
	plan replydomain.ReplyPlan
}

func (p fixedPlanner) Plan(_ context.Context, _ conversationdomain.ContextSnapshot, _ policydomain.AutonomyDecision) (replydomain.ReplyPlan, error) {
	return p.plan, nil
}

func TestAdapterResolvesPlannedActionBeforeExecution(t *testing.T) {
	tests := []struct {
		name       string
		event      conversationdomain.ConversationEvent
		planned    policydomain.DecisionAction
		wantAction policydomain.DecisionAction
	}{
		{name: "text reply", event: directedMessage(), planned: policydomain.ActionReply, wantAction: policydomain.ActionReply},
		{name: "stay silent", event: directedMessage(), planned: policydomain.ActionSilent, wantAction: policydomain.ActionSilent},
		{name: "reaction", event: directedMessage(), planned: policydomain.ActionReact, wantAction: policydomain.ActionReact},
		{name: "meme", event: directedMessage(), planned: policydomain.ActionMemeOnly, wantAction: policydomain.ActionMemeOnly},
		{name: "repair", event: directedMessage(), planned: policydomain.ActionRepair, wantAction: policydomain.ActionRepair},
		{
			name:       "poke back",
			event:      conversationdomain.ConversationEvent{EventID: "poke", GroupID: 1, UserID: 2, Kind: conversationdomain.EventPoke},
			planned:    policydomain.ActionPokeBack,
			wantAction: policydomain.ActionPokeBack,
		},
		{
			name:       "poke back rejected outside poke trigger",
			event:      directedMessage(),
			planned:    policydomain.ActionPokeBack,
			wantAction: policydomain.ActionReply,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := NewAdapter(fixedSnapshotBuilder{}, fixedPlanner{plan: replydomain.ReplyPlan{
				PlannedActions: []policydomain.DecisionAction{test.planned},
				SendMode:       "group",
			}})
			result, err := adapter.Deliberate(context.Background(), Input{Envelope: conversationdomain.EventEnvelope{
				TraceID: "trace",
				Event:   test.event,
			}})
			if err != nil {
				t.Fatalf("Deliberate: %v", err)
			}
			if result.Decision.Action != test.wantAction {
				t.Fatalf("resolved action=%q want=%q", result.Decision.Action, test.wantAction)
			}
			if result.Thought.ChosenAction != string(test.wantAction) {
				t.Fatalf("thought action=%q want=%q", result.Thought.ChosenAction, test.wantAction)
			}
		})
	}
}

func TestDecisionForRecognizesPokeAndNoticeTriggers(t *testing.T) {
	for _, test := range []struct {
		name        string
		kind        conversationdomain.EventKind
		wantTrigger string
		wantAction  policydomain.DecisionAction
	}{
		{name: "poke", kind: conversationdomain.EventPoke, wantTrigger: "poke_reply", wantAction: policydomain.ActionPokeReply},
		{name: "notice", kind: conversationdomain.EventNotice, wantTrigger: "acknowledge", wantAction: policydomain.ActionReply},
	} {
		t.Run(test.name, func(t *testing.T) {
			decision := decisionFor(conversationdomain.EventEnvelope{
				TraceID: "trace",
				Event: conversationdomain.ConversationEvent{
					Kind: test.kind,
				},
			})
			if decision.TriggerType != test.wantTrigger || decision.Action != test.wantAction {
				t.Fatalf("unexpected decision: %+v", decision)
			}
		})
	}
}

func directedMessage() conversationdomain.ConversationEvent {
	return conversationdomain.ConversationEvent{
		EventID:      "message",
		GroupID:      1,
		UserID:       2,
		Kind:         conversationdomain.EventMessage,
		MentionedBot: true,
	}
}
