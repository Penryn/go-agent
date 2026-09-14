package prompting

import (
	"testing"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	"github.com/stretchr/testify/assert"
)

func TestStaticInstructionKeepsIdentityInCharacter(t *testing.T) {
	instruction := NewComposer(personadomain.PersonaConfig{Name: "芙芙"}).StaticInstruction()
	assert.Contains(t, instruction, "被直接问到身份时都保持人物口吻")
	assert.Contains(t, instruction, "不得把虚构经历包装成现实世界的可验证事实")
}

func TestMessagesIncludeCurrentEvent(t *testing.T) {
	composer := NewComposer(personadomain.PersonaConfig{Name: "芙芙"})
	snapshot := conversationdomain.ContextSnapshot{
		SelfID: 99,
		Event:  conversationdomain.ConversationEvent{EventID: "evt-1", UserID: 7, Text: "你在吗？"},
		PersonaState: personadomain.PersonaState{
			Mood:   string(personadomain.MoodSteady),
			Energy: string(personadomain.EnergyNormal),
		},
	}
	messages := composer.Messages(snapshot, policydomain.AutonomyDecision{TriggerType: "question"})
	if len(messages) < 2 {
		t.Fatal("expected current event and context messages")
	}
	assert.Contains(t, messages[len(messages)-2].Content, "你在吗？")
}

func TestResponseScenariosAreSelectedPerTurn(t *testing.T) {
	persona := personadomain.PersonaConfig{
		Name: "芙芙",
		ResponseScenarios: []personadomain.ResponseScenario{
			{Situation: "被问到技术问题", Rules: []string{"不确定时先查证"}},
			{Situation: "自己的生活状态变化", Rules: []string{"只记录可靠变化"}},
		},
	}
	composer := NewComposer(persona)
	if assert.NotContains(t, composer.StaticInstruction(), "被问到技术问题") {
		question := composer.DynamicInstruction(conversationdomain.ContextSnapshot{}, policydomain.AutonomyDecision{TriggerType: "question"})
		assert.Contains(t, question, "被问到技术问题")
		assert.NotContains(t, question, "自己的生活状态变化")
	}
}
