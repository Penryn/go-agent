package prompting

import (
	"strings"
	"testing"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
)

func TestInstructionIncludesPersonaState(t *testing.T) {
	c := NewComposer(defaultPersona())

	snapshot := conversationdomain.ContextSnapshot{
		PersonaState: personadomain.PersonaState{
			Mood:   "excited",
			Energy: "high",
		},
		RelationshipState: profiledomain.RelationshipState{
			Familiarity: 0.75,
			Affinity:    0.60,
		},
	}

	instruction := c.Instruction(snapshot, policydomain.AutonomyDecision{})

	if !strings.Contains(instruction, "心情=excited") {
		t.Fatalf("expected mood in instruction, got:\n%s", instruction)
	}
	if !strings.Contains(instruction, "精力=high") {
		t.Fatalf("expected energy in instruction, got:\n%s", instruction)
	}
	if !strings.Contains(instruction, "熟悉度=0.75") {
		t.Fatalf("expected familiarity in instruction, got:\n%s", instruction)
	}
	if !strings.Contains(instruction, "好感度=0.60") {
		t.Fatalf("expected affinity in instruction, got:\n%s", instruction)
	}
}

func TestInstructionDefaultsEmptyMoodAndEnergy(t *testing.T) {
	c := NewComposer(defaultPersona())

	snapshot := conversationdomain.ContextSnapshot{}

	instruction := c.Instruction(snapshot, policydomain.AutonomyDecision{})

	if !strings.Contains(instruction, "心情=steady") {
		t.Fatalf("expected default mood 'steady', got:\n%s", instruction)
	}
	if !strings.Contains(instruction, "精力=normal") {
		t.Fatalf("expected default energy 'normal', got:\n%s", instruction)
	}
}
