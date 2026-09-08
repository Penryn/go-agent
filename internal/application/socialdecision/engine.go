// Package socialdecision 实现社交决策引擎，决定是否参与和社交意图。
package socialdecision

import (
	"context"
	"time"

	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
	relationshipdomain "github.com/phlin/go-agent/internal/domain/relationship"
	scenedomain "github.com/phlin/go-agent/internal/domain/scene"
)

// DecisionEngine 社交决策引擎
type DecisionEngine struct {
	// 依赖的服务（暂时使用接口占位）
	sceneStore        SceneStore
	relationshipStore RelationshipStore
	postureStore      PostureStore
	ephemeralStore    EphemeralStateStore

	// 决策配置
	config DecisionConfig
}

// DecisionConfig 决策引擎配置
type DecisionConfig struct {
	// 硬规则阈值
	CooldownSeconds       int     `json:"cooldown_seconds"`
	ConsecutiveLimit      int     `json:"consecutive_limit"`
	EventExpirySeconds    int     `json:"event_expiry_seconds"`

	// 场景判断阈值
	FastConversationGap   int     `json:"fast_conversation_gap"` // 秒
	MinTopicRelevance     float64 `json:"min_topic_relevance"`

	// 关系判断阈值
	MinFamiliarity        float64 `json:"min_familiarity"`
	MaxFriction           float64 `json:"max_friction"`
	MinTrust              float64 `json:"min_trust"`

	// 人格判断阈值
	MinEnergy             float64 `json:"min_energy"` // Energy 映射到 0-1
	MinSocialPatience     float64 `json:"min_social_patience"`

	// 模型判断阈值
	MinConfidence         float64 `json:"min_confidence"`
}

// NewDecisionEngine 创建决策引擎
func NewDecisionEngine(
	sceneStore SceneStore,
	relationshipStore RelationshipStore,
	postureStore PostureStore,
	ephemeralStore EphemeralStateStore,
	config DecisionConfig,
) *DecisionEngine {
	return &DecisionEngine{
		sceneStore:        sceneStore,
		relationshipStore: relationshipStore,
		postureStore:      postureStore,
		ephemeralStore:    ephemeralStore,
		config:            config,
	}
}

// DecideParticipation 执行完整的社交决策流程
func (e *DecisionEngine) DecideParticipation(
	ctx context.Context,
	req DecisionRequest,
) (*presencedomain.ParticipationDecision, error) {
	now := time.Now()

	decision := &presencedomain.ParticipationDecision{
		DecisionID:     generateDecisionID(),
		GroupID:        req.GroupID,
		TriggerEventID: req.TriggerEventID,
		DecidedAt:      now,
		Participate:    false,
		RuleHits:       []string{},
		ExpiresAt:      now.Add(time.Duration(e.config.EventExpirySeconds) * time.Second),
	}

	// 步骤 1: 硬规则过滤
	if blocked, reason := e.checkHardRules(ctx, req); blocked {
		decision.ReasonCode = reason
		decision.RuleHits = append(decision.RuleHits, reason)
		return decision, nil
	}

	// 步骤 2: 场景判断
	scene, err := e.sceneStore.GetGroupScene(ctx, req.GroupID)
	if err != nil {
		return nil, err
	}
	decision.SceneSnapshot = scene

	if blocked, reason := e.checkSceneContext(scene, req); blocked {
		decision.ReasonCode = reason
		decision.RuleHits = append(decision.RuleHits, reason)
		return decision, nil
	}

	// 步骤 3: 关系判断
	var relationship *relationshipdomain.State
	if req.TargetUserID > 0 {
		relationship, err = e.relationshipStore.GetRelationship(ctx, req.PersonaID, req.GroupID, req.TargetUserID)
		if err != nil {
			return nil, err
		}
		decision.RelationshipSnapshot = relationship
		decision.TargetUserID = req.TargetUserID

		if blocked, reason := e.checkRelationship(relationship); blocked {
			decision.ReasonCode = reason
			decision.RuleHits = append(decision.RuleHits, reason)
			return decision, nil
		}
	}

	// 步骤 4: 人格状态判断
	posture, err := e.postureStore.GetGroupPosture(ctx, req.PersonaID, req.GroupID)
	if err != nil {
		return nil, err
	}

	ephemeral, err := e.ephemeralStore.GetEphemeralState(ctx, req.PersonaID, req.GroupID)
	if err != nil {
		return nil, err
	}

	if blocked, reason := e.checkPersonaState(posture, ephemeral); blocked {
		decision.ReasonCode = reason
		decision.RuleHits = append(decision.RuleHits, reason)
		return decision, nil
	}

	// 步骤 5: 模型判断（可选，由调用方决定是否需要）
	// 如果前四步都通过，则 Participate = true，并填充社交意图
	decision.Participate = true
	decision.ReasonCode = e.inferReasonCode(req, scene, relationship)
	decision.Intent = e.inferIntent(req, scene, relationship)
	decision.Audience = e.inferAudience(req, relationship)
	decision.SocialValue = e.estimateSocialValue(scene, relationship, posture)
	decision.InterruptionRisk = e.estimateInterruptionRisk(scene)
	decision.ConfidenceScore = 0.8 // 规则通过默认高置信度

	return decision, nil
}

// checkHardRules 硬规则过滤
func (e *DecisionEngine) checkHardRules(ctx context.Context, req DecisionRequest) (bool, string) {
	// 检查黑名单
	if req.IsBlacklisted {
		return true, presencedomain.ReasonBlacklisted
	}

	// 检查自身消息
	if req.IsSelfMessage {
		return true, presencedomain.ReasonSelfMessage
	}

	// 检查冷却时间
	if req.SecondsSinceLastBotMessage < e.config.CooldownSeconds {
		return true, presencedomain.ReasonCooldown
	}

	// 检查连续发言限制
	if req.ConsecutiveBotMessages >= e.config.ConsecutiveLimit {
		return true, presencedomain.ReasonConsecutiveLimit
	}

	// 检查事件过期
	if req.EventAge > time.Duration(e.config.EventExpirySeconds)*time.Second {
		return true, presencedomain.ReasonEventExpired
	}

	return false, ""
}

// checkSceneContext 场景判断
func (e *DecisionEngine) checkSceneContext(scene *scenedomain.GroupScene, req DecisionRequest) (bool, string) {
	if scene == nil {
		return false, ""
	}

	// 检查是否在快速对话中
	if req.IsFastConversation {
		return true, presencedomain.ReasonFastConversation
	}

	// 检查话题是否已关闭
	if len(scene.OpenLoops) == 0 && scene.CurrentTopic == "" && !req.IsDirectMention {
		return true, presencedomain.ReasonTopicClosed
	}

	return false, ""
}

// checkRelationship 关系判断
func (e *DecisionEngine) checkRelationship(rel *relationshipdomain.State) (bool, string) {
	if rel == nil {
		return false, ""
	}

	// 检查熟悉度
	if rel.Familiarity < e.config.MinFamiliarity && rel.Trust < e.config.MinTrust {
		return true, presencedomain.ReasonLowFamiliarity
	}

	// 检查摩擦度
	if rel.Friction > e.config.MaxFriction {
		return true, presencedomain.ReasonHighFriction
	}

	return false, ""
}

// checkPersonaState 人格状态判断
func (e *DecisionEngine) checkPersonaState(
	posture *personadomain.GroupPosture,
	ephemeral *personadomain.EphemeralState,
) (bool, string) {
	if ephemeral == nil {
		return false, ""
	}

	// 检查精力
	energyScore := mapEnergyToScore(ephemeral.Energy)
	if energyScore < e.config.MinEnergy {
		return true, presencedomain.ReasonLowEnergy
	}

	// 检查社交耐心
	if ephemeral.SocialPatience < e.config.MinSocialPatience {
		return true, presencedomain.ReasonLowPatience
	}

	// 检查群姿态
	if posture != nil && posture.ParticipationBias < -0.5 {
		return true, presencedomain.ReasonPostureWithdrawn
	}

	return false, ""
}

// inferReasonCode 推断参与原因
func (e *DecisionEngine) inferReasonCode(
	req DecisionRequest,
	scene *scenedomain.GroupScene,
	rel *relationshipdomain.State,
) string {
	if req.IsDirectMention {
		return presencedomain.ReasonDirectMention
	}
	if req.IsQuestionToBot {
		return presencedomain.ReasonQuestionToBot
	}
	if rel != nil && rel.Affinity > 0.7 {
		return presencedomain.ReasonRelationshipGood
	}
	if scene != nil && len(scene.OpenLoops) > 0 {
		return presencedomain.ReasonTopicMatch
	}
	return presencedomain.ReasonOpportuneMoment
}

// inferIntent 推断社交意图
func (e *DecisionEngine) inferIntent(
	req DecisionRequest,
	scene *scenedomain.GroupScene,
	rel *relationshipdomain.State,
) string {
	if req.IsDirectMention || req.IsQuestionToBot {
		return "respond"
	}
	if scene != nil && len(scene.OpenLoops) > 0 {
		return "continue"
	}
	if scene != nil && scene.ConflictLevel > 0.5 {
		return "moderate"
	}
	return "observe"
}

// inferAudience 推断受众
func (e *DecisionEngine) inferAudience(req DecisionRequest, rel *relationshipdomain.State) string {
	if req.IsDirectMention {
		return "target_only"
	}
	return "group"
}

// estimateSocialValue 估算社交价值
func (e *DecisionEngine) estimateSocialValue(
	scene *scenedomain.GroupScene,
	rel *relationshipdomain.State,
	posture *personadomain.GroupPosture,
) float64 {
	value := 0.5 // 基线

	if rel != nil {
		value += rel.Affinity * 0.3
	}

	if posture != nil {
		value += posture.ParticipationBias * 0.2
	}

	if scene != nil && scene.BotReception == "positive" {
		value += 0.2
	}

	return clamp(value, -1.0, 1.0)
}

// estimateInterruptionRisk 估算打断风险
func (e *DecisionEngine) estimateInterruptionRisk(scene *scenedomain.GroupScene) float64 {
	if scene == nil {
		return 0.3
	}

	risk := 0.0

	// 活跃度越高，打断风险越高
	risk += scene.ActivityLevel * 0.4

	// 冲突中打断风险更高
	risk += scene.ConflictLevel * 0.3

	return clamp(risk, 0.0, 1.0)
}

// 辅助函数
func mapEnergyToScore(energy personadomain.Energy) float64 {
	switch energy {
	case personadomain.EnergyHigh:
		return 1.0
	case personadomain.EnergyNormal:
		return 0.7
	case personadomain.EnergyLow:
		return 0.4
	case personadomain.EnergyTired:
		return 0.1
	default:
		return 0.5
	}
}

func clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func generateDecisionID() string {
	return "dec_" + time.Now().Format("20060102150405") + "_" + randomString(8)
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
	}
	return string(b)
}
