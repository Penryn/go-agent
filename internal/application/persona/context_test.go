package persona

import (
	"context"
	"testing"
	"time"

	personadomain "github.com/phlin/go-agent/internal/domain/persona"
)

// Mock stores for testing
type mockPostureStore struct {
	posture *personadomain.GroupPosture
	updated bool
}

func (m *mockPostureStore) GetGroupPosture(ctx context.Context, personaID string, groupID int64) (*personadomain.GroupPosture, error) {
	if m.posture == nil {
		return nil, nil // 模拟不存在
	}
	return m.posture, nil
}

func (m *mockPostureStore) UpdateGroupPosture(ctx context.Context, posture *personadomain.GroupPosture) error {
	m.posture = posture
	m.updated = true
	return nil
}

type mockEphemeralStore struct {
	state   *personadomain.EphemeralState
	updated bool
}

func (m *mockEphemeralStore) GetEphemeralState(ctx context.Context, personaID string, groupID int64) (*personadomain.EphemeralState, error) {
	if m.state == nil {
		return nil, nil // 模拟不存在
	}
	return m.state, nil
}

func (m *mockEphemeralStore) UpdateEphemeralState(ctx context.Context, state *personadomain.EphemeralState) error {
	m.state = state
	m.updated = true
	return nil
}

type mockFactStore struct {
	facts []personadomain.PersonaFact
}

func (m *mockFactStore) GetCanonicalFacts(ctx context.Context, personaID string) ([]personadomain.PersonaFact, error) {
	return m.facts, nil
}

func TestContextAssembler_CreateDefaultPosture(t *testing.T) {
	postureStore := &mockPostureStore{}
	ephemeralStore := &mockEphemeralStore{}
	factStore := &mockFactStore{}

	assembler := NewContextAssembler(postureStore, ephemeralStore, factStore)

	identity := personadomain.PersonaConfig{
		ID:   "test_persona",
		Name: "测试角色",
	}

	ctx, err := assembler.AssembleContext(context.Background(), identity, 123456)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 验证创建了默认姿态
	if !postureStore.updated {
		t.Error("expected posture to be created")
	}

	if ctx.Posture.PersonaID != "test_persona" {
		t.Errorf("expected PersonaID test_persona, got %s", ctx.Posture.PersonaID)
	}

	if ctx.Posture.GroupID != 123456 {
		t.Errorf("expected GroupID 123456, got %d", ctx.Posture.GroupID)
	}

	// 验证默认值
	if ctx.Posture.Familiarity != 0.3 {
		t.Errorf("expected default Familiarity 0.3, got %f", ctx.Posture.Familiarity)
	}
}

func TestContextAssembler_CreateDefaultEphemeral(t *testing.T) {
	postureStore := &mockPostureStore{}
	ephemeralStore := &mockEphemeralStore{}
	factStore := &mockFactStore{}

	assembler := NewContextAssembler(postureStore, ephemeralStore, factStore)

	identity := personadomain.PersonaConfig{
		ID:   "test_persona",
		Name: "测试角色",
	}

	ctx, err := assembler.AssembleContext(context.Background(), identity, 123456)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 验证创建了默认即时状态
	if !ephemeralStore.updated {
		t.Error("expected ephemeral state to be created")
	}

	if ctx.EphemeralState.Mood != personadomain.MoodSteady {
		t.Errorf("expected default Mood steady, got %s", ctx.EphemeralState.Mood)
	}

	if ctx.EphemeralState.Energy != personadomain.EnergyNormal {
		t.Errorf("expected default Energy normal, got %s", ctx.EphemeralState.Energy)
	}
}

func TestContextAssembler_UseExistingPosture(t *testing.T) {
	existingPosture := personadomain.GroupPosture{
		PersonaID:         "test_persona",
		GroupID:           123456,
		Familiarity:       0.8,
		ParticipationBias: 0.5,
		HumorLevel:        0.7,
		UpdatedAt:         time.Now(),
		Revision:          5,
	}

	postureStore := &mockPostureStore{posture: &existingPosture}
	ephemeralStore := &mockEphemeralStore{}
	factStore := &mockFactStore{}

	assembler := NewContextAssembler(postureStore, ephemeralStore, factStore)

	identity := personadomain.PersonaConfig{
		ID:   "test_persona",
		Name: "测试角色",
	}

	ctx, err := assembler.AssembleContext(context.Background(), identity, 123456)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 验证使用了现有姿态
	if ctx.Posture.Familiarity != 0.8 {
		t.Errorf("expected Familiarity 0.8, got %f", ctx.Posture.Familiarity)
	}

	if ctx.Posture.Revision != 5 {
		t.Errorf("expected Revision 5, got %d", ctx.Posture.Revision)
	}
}

func TestContextAssembler_ExpiredEphemeralState(t *testing.T) {
	expiredState := personadomain.EphemeralState{
		PersonaID:      "test_persona",
		GroupID:        123456,
		Mood:           personadomain.MoodHappy,
		Energy:         personadomain.EnergyHigh,
		SocialPatience: 0.5,
		UpdatedAt:      time.Now().Add(-2 * time.Hour),
		ExpiresAt:      time.Now().Add(-1 * time.Hour), // 已过期
	}

	postureStore := &mockPostureStore{}
	ephemeralStore := &mockEphemeralStore{state: &expiredState}
	factStore := &mockFactStore{}

	assembler := NewContextAssembler(postureStore, ephemeralStore, factStore)

	identity := personadomain.PersonaConfig{
		ID:   "test_persona",
		Name: "测试角色",
	}

	ctx, err := assembler.AssembleContext(context.Background(), identity, 123456)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 验证重置为基线状态
	if ctx.EphemeralState.Mood != personadomain.MoodSteady {
		t.Errorf("expected reset to Mood steady, got %s", ctx.EphemeralState.Mood)
	}

	if ctx.EphemeralState.Energy != personadomain.EnergyNormal {
		t.Errorf("expected reset to Energy normal, got %s", ctx.EphemeralState.Energy)
	}
}

func TestContextAssembler_LoadCanonicalFacts(t *testing.T) {
	postureStore := &mockPostureStore{}
	ephemeralStore := &mockEphemeralStore{}
	factStore := &mockFactStore{
		facts: []personadomain.PersonaFact{
			{Key: "name", Value: "小明", Confidence: 1.0},
			{Key: "location", Value: "北京", Confidence: 0.9},
			{Key: "hobby", Value: "编程", Confidence: 0.8},
		},
	}

	assembler := NewContextAssembler(postureStore, ephemeralStore, factStore)

	identity := personadomain.PersonaConfig{
		ID:   "test_persona",
		Name: "测试角色",
	}

	ctx, err := assembler.AssembleContext(context.Background(), identity, 123456)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 验证加载了 canonical facts
	if len(ctx.CanonicalFacts) != 3 {
		t.Errorf("expected 3 facts, got %d", len(ctx.CanonicalFacts))
	}

	if ctx.CanonicalFacts["name"] != "小明" {
		t.Errorf("expected name=小明, got %s", ctx.CanonicalFacts["name"])
	}

	if ctx.CanonicalFacts["location"] != "北京" {
		t.Errorf("expected location=北京, got %s", ctx.CanonicalFacts["location"])
	}
}

func TestContextAssembler_UpdatePosture(t *testing.T) {
	postureStore := &mockPostureStore{}
	ephemeralStore := &mockEphemeralStore{}
	factStore := &mockFactStore{}

	assembler := NewContextAssembler(postureStore, ephemeralStore, factStore)

	posture := personadomain.GroupPosture{
		PersonaID:         "test_persona",
		GroupID:           123456,
		Familiarity:       0.5,
		ParticipationBias: 0.2,
		UpdatedAt:         time.Now(),
		Revision:          1,
	}

	err := assembler.UpdatePosture(context.Background(), &posture)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 验证 Revision 递增
	if posture.Revision != 2 {
		t.Errorf("expected Revision 2, got %d", posture.Revision)
	}

	// 验证存储被调用
	if !postureStore.updated {
		t.Error("expected posture to be updated")
	}
}

func TestContextAssembler_UpdateEphemeralState(t *testing.T) {
	postureStore := &mockPostureStore{}
	ephemeralStore := &mockEphemeralStore{}
	factStore := &mockFactStore{}

	assembler := NewContextAssembler(postureStore, ephemeralStore, factStore)

	state := personadomain.EphemeralState{
		PersonaID:      "test_persona",
		GroupID:        123456,
		Mood:           personadomain.MoodHappy,
		Energy:         personadomain.EnergyHigh,
		SocialPatience: 0.9,
		UpdatedAt:      time.Now().Add(-1 * time.Hour),
		ExpiresAt:      time.Now().Add(1 * time.Hour),
	}

	oldUpdateTime := state.UpdatedAt

	err := assembler.UpdateEphemeralState(context.Background(), &state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 验证 UpdatedAt 更新
	if !state.UpdatedAt.After(oldUpdateTime) {
		t.Error("expected UpdatedAt to be refreshed")
	}

	// 验证存储被调用
	if !ephemeralStore.updated {
		t.Error("expected ephemeral state to be updated")
	}
}
