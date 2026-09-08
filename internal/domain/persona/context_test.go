package persona

import (
	"testing"
	"time"
)

func TestDefaultGroupPosture(t *testing.T) {
	personaID := "test_persona"
	groupID := int64(123456)

	posture := DefaultGroupPosture(personaID, groupID)

	if posture.PersonaID != personaID {
		t.Errorf("expected PersonaID %s, got %s", personaID, posture.PersonaID)
	}

	if posture.GroupID != groupID {
		t.Errorf("expected GroupID %d, got %d", groupID, posture.GroupID)
	}

	if posture.Familiarity != 0.3 {
		t.Errorf("expected Familiarity 0.3, got %f", posture.Familiarity)
	}

	if posture.ParticipationBias != 0.0 {
		t.Errorf("expected ParticipationBias 0.0, got %f", posture.ParticipationBias)
	}

	if posture.Revision != 1 {
		t.Errorf("expected Revision 1, got %d", posture.Revision)
	}
}

func TestDefaultEphemeralState(t *testing.T) {
	personaID := "test_persona"
	groupID := int64(123456)

	state := DefaultEphemeralState(personaID, groupID)

	if state.PersonaID != personaID {
		t.Errorf("expected PersonaID %s, got %s", personaID, state.PersonaID)
	}

	if state.GroupID != groupID {
		t.Errorf("expected GroupID %d, got %d", groupID, state.GroupID)
	}

	if state.Mood != MoodSteady {
		t.Errorf("expected Mood %s, got %s", MoodSteady, state.Mood)
	}

	if state.Energy != EnergyNormal {
		t.Errorf("expected Energy %s, got %s", EnergyNormal, state.Energy)
	}

	if state.SocialPatience != 0.8 {
		t.Errorf("expected SocialPatience 0.8, got %f", state.SocialPatience)
	}

	if state.ExpiresAt.Before(time.Now()) {
		t.Error("expected ExpiresAt to be in the future")
	}
}

func TestPersonaContext(t *testing.T) {
	identity := PersonaConfig{
		ID:   "test_persona",
		Name: "测试角色",
	}

	posture := DefaultGroupPosture("test_persona", 123456)
	ephemeral := DefaultEphemeralState("test_persona", 123456)

	ctx := PersonaContext{
		Identity:       identity,
		Posture:        posture,
		EphemeralState: ephemeral,
		CanonicalFacts: map[string]string{
			"name":     "小明",
			"location": "北京",
		},
	}

	if ctx.Identity.ID != "test_persona" {
		t.Error("Identity not set correctly")
	}

	if ctx.Posture.GroupID != 123456 {
		t.Error("Posture not set correctly")
	}

	if ctx.EphemeralState.Mood != MoodSteady {
		t.Error("EphemeralState not set correctly")
	}

	if ctx.CanonicalFacts["name"] != "小明" {
		t.Error("CanonicalFacts not set correctly")
	}
}
