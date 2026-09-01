package persona

import (
	"testing"
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
)

func TestTransitionStatePrioritizesFloodOverDirectEngagement(t *testing.T) {
	current := personadomain.PersonaState{Mood: string(personadomain.MoodSteady), Energy: string(personadomain.EnergyNormal)}
	snapshot := conversationdomain.ContextSnapshot{Event: conversationdomain.ConversationEvent{MentionedBot: true}, RuntimeState: policydomain.RuntimeState{RepliesLast10Min: 5}}
	mood, _, bias := transitionState(current, snapshot, policydomain.AutonomyDecision{Action: policydomain.ActionReply, TriggerType: "answer"}, true)
	if mood != personadomain.MoodAggro || bias >= 0 {
		t.Fatalf("flood transition = mood %q bias %v", mood, bias)
	}
}

func TestTransitionStateUsesSemanticInteraction(t *testing.T) {
	current := personadomain.PersonaState{Mood: string(personadomain.MoodSteady), Energy: string(personadomain.EnergyNormal)}
	snapshot := conversationdomain.ContextSnapshot{RelationshipState: profiledomain.RelationshipState{Affinity: 0.2}}
	mood, _, bias := transitionState(current, snapshot, policydomain.AutonomyDecision{Action: policydomain.ActionReply, TriggerType: "banter"}, true)
	if mood != personadomain.MoodHappy || bias <= 0 {
		t.Fatalf("banter transition = mood %q bias %v", mood, bias)
	}
	snapshot.Event.MentionedBot = true
	mood, _, _ = transitionState(current, snapshot, policydomain.AutonomyDecision{Action: policydomain.ActionReply, TriggerType: "support"}, true)
	if mood != personadomain.MoodSteady {
		t.Fatalf("support transition = mood %q, want steady", mood)
	}
}

func TestTransitionStateClampsTalkBias(t *testing.T) {
	current := personadomain.PersonaState{Mood: string(personadomain.MoodSteady), Energy: string(personadomain.EnergyNormal), TalkBias: 0.49}
	snapshot := conversationdomain.ContextSnapshot{RelationshipState: profiledomain.RelationshipState{Affinity: 0.8}}
	_, _, bias := transitionState(current, snapshot, policydomain.AutonomyDecision{Action: policydomain.ActionReply, TriggerType: "banter"}, true)
	if bias != 0.5 {
		t.Fatalf("bias = %v, want 0.5", bias)
	}
}

func TestLowEngagementUsesElapsedTime(t *testing.T) {
	snapshot := conversationdomain.ContextSnapshot{RuntimeState: policydomain.RuntimeState{LastBotSpeakAt: time.Now().Add(-3 * time.Hour)}}
	if !isLowEngagement(snapshot) {
		t.Fatal("expected low engagement")
	}
}
