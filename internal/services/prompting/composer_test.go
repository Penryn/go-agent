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

func TestInstructionUsesGroupPersonaOverlay(t *testing.T) {
	c := NewComposer(defaultPersona())
	snapshot := conversationdomain.ContextSnapshot{
		Event: conversationdomain.ConversationEvent{GroupID: 42},
		GroupPolicy: policydomain.GroupPolicy{PersonaOverlay: map[string]any{
			"name":         "群里的艾莲",
			"speech_style": "只说半句",
		}},
	}

	instruction := c.Instruction(snapshot, policydomain.AutonomyDecision{})
	if !strings.Contains(instruction, "你是 群里的艾莲") {
		t.Fatalf("expected group persona name, got:\n%s", instruction)
	}
	if !strings.Contains(instruction, "说话风格: 只说半句") {
		t.Fatalf("expected group speech style, got:\n%s", instruction)
	}
}

func TestMessagesTruncateOldContextButKeepCurrentEvent(t *testing.T) {
	c := NewComposer(defaultPersona())
	c.SetContextBudgets(80, 100)
	snapshot := conversationdomain.ContextSnapshot{
		Event: conversationdomain.ConversationEvent{EventID: "current", MessageID: "m-current", UserID: 7, TimestampUnix: 100},
		RecentTurns: []conversationdomain.ConversationEvent{
			{EventID: "old", MessageID: "m-old", UserID: 8, Text: strings.Repeat("旧", 80), TimestampUnix: 90},
			{EventID: "current", MessageID: "m-current", UserID: 7, Text: "当前消息", TimestampUnix: 100},
		},
	}
	messages := c.Messages(snapshot)
	content := messages[0].Content
	if !strings.Contains(content, "当前消息") {
		t.Fatalf("current event was dropped: %s", content)
	}
	if !strings.Contains(content, "较早上下文已裁剪") {
		t.Fatalf("expected truncation marker: %s", content)
	}
}
