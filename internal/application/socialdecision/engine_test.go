package socialdecision

import (
	"context"
	"testing"

	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
	relationshipdomain "github.com/phlin/go-agent/internal/domain/relationship"
	scenedomain "github.com/phlin/go-agent/internal/domain/scene"
)

// Mock stores for testing
type mockSceneStore struct {
	scene *scenedomain.GroupScene
}

func (m *mockSceneStore) GetGroupScene(ctx context.Context, groupID int64) (*scenedomain.GroupScene, error) {
	return m.scene, nil
}

func (m *mockSceneStore) UpdateGroupScene(ctx context.Context, scene *scenedomain.GroupScene) error {
	m.scene = scene
	return nil
}

type mockRelationshipStore struct {
	relationship *relationshipdomain.State
}

func (m *mockRelationshipStore) GetRelationship(ctx context.Context, personaID string, groupID, userID int64) (*relationshipdomain.State, error) {
	return m.relationship, nil
}

type mockPostureStore struct {
	posture *personadomain.GroupPosture
}

func (m *mockPostureStore) GetGroupPosture(ctx context.Context, personaID string, groupID int64) (*personadomain.GroupPosture, error) {
	return m.posture, nil
}

func (m *mockPostureStore) UpdateGroupPosture(ctx context.Context, posture *personadomain.GroupPosture) error {
	m.posture = posture
	return nil
}

type mockEphemeralStore struct {
	state *personadomain.EphemeralState
}

func (m *mockEphemeralStore) GetEphemeralState(ctx context.Context, personaID string, groupID int64) (*personadomain.EphemeralState, error) {
	return m.state, nil
}

func (m *mockEphemeralStore) UpdateEphemeralState(ctx context.Context, state *personadomain.EphemeralState) error {
	m.state = state
	return nil
}

func TestDecisionEngine_BlockedBySelfMessage(t *testing.T) {
	engine := NewDecisionEngine(
		&mockSceneStore{},
		&mockRelationshipStore{},
		&mockPostureStore{},
		&mockEphemeralStore{},
		DefaultDecisionConfig(),
	)

	req := DecisionRequest{
		PersonaID:      "test_persona",
		GroupID:        123456,
		TriggerEventID: "evt_123",
		IsSelfMessage:  true,
	}

	decision, err := engine.DecideParticipation(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if decision.Participate {
		t.Error("expected Participate to be false for self message")
	}

	if decision.ReasonCode != presencedomain.ReasonSelfMessage {
		t.Errorf("expected ReasonCode %s, got %s", presencedomain.ReasonSelfMessage, decision.ReasonCode)
	}
}

func TestDecisionEngine_BlockedByCooldown(t *testing.T) {
	engine := NewDecisionEngine(
		&mockSceneStore{},
		&mockRelationshipStore{},
		&mockPostureStore{},
		&mockEphemeralStore{},
		DefaultDecisionConfig(),
	)

	req := DecisionRequest{
		PersonaID:                  "test_persona",
		GroupID:                    123456,
		TriggerEventID:             "evt_123",
		SecondsSinceLastBotMessage: 5, // 少于默认的 10 秒冷却
	}

	decision, err := engine.DecideParticipation(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if decision.Participate {
		t.Error("expected Participate to be false for cooldown")
	}

	if decision.ReasonCode != presencedomain.ReasonCooldown {
		t.Errorf("expected ReasonCode %s, got %s", presencedomain.ReasonCooldown, decision.ReasonCode)
	}
}

func TestDecisionEngine_BlockedByLowEnergy(t *testing.T) {
	engine := NewDecisionEngine(
		&mockSceneStore{scene: &scenedomain.GroupScene{
			CurrentTopic: "有话题", // 确保场景不阻塞
			OpenLoops:    []string{"话题1"},
		}},
		&mockRelationshipStore{relationship: &relationshipdomain.State{Familiarity: 0.5}},
		&mockPostureStore{posture: &personadomain.GroupPosture{ParticipationBias: 0.0}},
		&mockEphemeralStore{state: &personadomain.EphemeralState{
			Energy:         personadomain.EnergyTired, // 疲劳状态
			SocialPatience: 0.8,
		}},
		DefaultDecisionConfig(),
	)

	req := DecisionRequest{
		PersonaID:                  "test_persona",
		GroupID:                    123456,
		TriggerEventID:             "evt_123",
		TargetUserID:               789, // 添加目标用户以触发关系检查
		SecondsSinceLastBotMessage: 20, // 冷却已过
	}

	decision, err := engine.DecideParticipation(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if decision.Participate {
		t.Error("expected Participate to be false for low energy")
	}

	if decision.ReasonCode != presencedomain.ReasonLowEnergy {
		t.Errorf("expected ReasonCode %s, got %s", presencedomain.ReasonLowEnergy, decision.ReasonCode)
	}
}

func TestDecisionEngine_AllowedByDirectMention(t *testing.T) {
	engine := NewDecisionEngine(
		&mockSceneStore{scene: &scenedomain.GroupScene{
			CurrentTopic: "讨论技术",
		}},
		&mockRelationshipStore{relationship: &relationshipdomain.State{
			Familiarity: 0.5,
			Affinity:    0.6,
			Trust:       0.5,
			Friction:    0.2,
		}},
		&mockPostureStore{posture: &personadomain.GroupPosture{
			ParticipationBias: 0.0,
		}},
		&mockEphemeralStore{state: &personadomain.EphemeralState{
			Energy:         personadomain.EnergyNormal,
			SocialPatience: 0.8,
		}},
		DefaultDecisionConfig(),
	)

	req := DecisionRequest{
		PersonaID:                  "test_persona",
		GroupID:                    123456,
		TriggerEventID:             "evt_123",
		TargetUserID:               789,
		SecondsSinceLastBotMessage: 20,
		IsDirectMention:            true, // 直接@
	}

	decision, err := engine.DecideParticipation(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !decision.Participate {
		t.Errorf("expected Participate to be true for direct mention, got false with reason: %s", decision.ReasonCode)
	}

	if decision.ReasonCode != presencedomain.ReasonDirectMention {
		t.Errorf("expected ReasonCode %s, got %s", presencedomain.ReasonDirectMention, decision.ReasonCode)
	}

	if decision.Intent != "respond" {
		t.Errorf("expected Intent 'respond', got %s", decision.Intent)
	}

	if decision.TargetUserID != 789 {
		t.Errorf("expected TargetUserID 789, got %d", decision.TargetUserID)
	}
}

func TestDecisionEngine_HighFrictionBlocks(t *testing.T) {
	engine := NewDecisionEngine(
		&mockSceneStore{scene: &scenedomain.GroupScene{
			CurrentTopic: "有话题", // 确保场景不阻塞
			OpenLoops:    []string{"话题1"},
		}},
		&mockRelationshipStore{relationship: &relationshipdomain.State{
			Familiarity: 0.5,
			Friction:    0.9, // 高摩擦
		}},
		&mockPostureStore{posture: &personadomain.GroupPosture{}},
		&mockEphemeralStore{state: &personadomain.EphemeralState{
			Energy:         personadomain.EnergyNormal,
			SocialPatience: 0.8,
		}},
		DefaultDecisionConfig(),
	)

	req := DecisionRequest{
		PersonaID:                  "test_persona",
		GroupID:                    123456,
		TriggerEventID:             "evt_123",
		TargetUserID:               789,
		SecondsSinceLastBotMessage: 20,
	}

	decision, err := engine.DecideParticipation(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if decision.Participate {
		t.Error("expected Participate to be false for high friction")
	}

	if decision.ReasonCode != presencedomain.ReasonHighFriction {
		t.Errorf("expected ReasonCode %s, got %s", presencedomain.ReasonHighFriction, decision.ReasonCode)
	}
}

func TestDecisionEngine_EstimateValues(t *testing.T) {
	engine := NewDecisionEngine(
		&mockSceneStore{scene: &scenedomain.GroupScene{
			ActivityLevel: 0.8,
			BotReception:  "positive",
		}},
		&mockRelationshipStore{relationship: &relationshipdomain.State{
			Familiarity: 0.7,
			Affinity:    0.8,
			Trust:       0.6,
		}},
		&mockPostureStore{posture: &personadomain.GroupPosture{
			ParticipationBias: 0.3,
		}},
		&mockEphemeralStore{state: &personadomain.EphemeralState{
			Energy:         personadomain.EnergyHigh,
			SocialPatience: 0.9,
		}},
		DefaultDecisionConfig(),
	)

	req := DecisionRequest{
		PersonaID:                  "test_persona",
		GroupID:                    123456,
		TriggerEventID:             "evt_123",
		TargetUserID:               789,
		SecondsSinceLastBotMessage: 20,
		IsDirectMention:            true,
	}

	decision, err := engine.DecideParticipation(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !decision.Participate {
		t.Fatalf("expected Participate to be true")
	}

	// 验证社交价值估算
	if decision.SocialValue <= 0.5 {
		t.Errorf("expected high SocialValue with good relationship, got %f", decision.SocialValue)
	}

	// 验证打断风险估算（高活跃度应该有较高风险）
	if decision.InterruptionRisk < 0.2 {
		t.Errorf("expected some InterruptionRisk with high activity, got %f", decision.InterruptionRisk)
	}

	// 验证置信度
	if decision.ConfidenceScore < 0.5 {
		t.Errorf("expected high ConfidenceScore for rule-based decision, got %f", decision.ConfidenceScore)
	}
}
