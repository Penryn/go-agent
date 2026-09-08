package socialdecision

import (
	"context"
	"time"

	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	relationshipdomain "github.com/phlin/go-agent/internal/domain/relationship"
	scenedomain "github.com/phlin/go-agent/internal/domain/scene"
)

// DecisionRequest 决策请求
type DecisionRequest struct {
	PersonaID      string `json:"persona_id"`
	GroupID        int64  `json:"group_id"`
	TriggerEventID string `json:"trigger_event_id"`
	TargetUserID   int64  `json:"target_user_id,omitempty"`

	// 硬规则检查项
	IsBlacklisted              bool          `json:"is_blacklisted"`
	IsSelfMessage              bool          `json:"is_self_message"`
	SecondsSinceLastBotMessage int           `json:"seconds_since_last_bot_message"`
	ConsecutiveBotMessages     int           `json:"consecutive_bot_messages"`
	EventAge                   time.Duration `json:"event_age"`

	// 场景检查项
	IsFastConversation bool `json:"is_fast_conversation"`
	IsDirectMention    bool `json:"is_direct_mention"`
	IsQuestionToBot    bool `json:"is_question_to_bot"`

	// 可选的上下文（如果已经加载）
	Scene        *scenedomain.GroupScene          `json:"scene,omitempty"`
	Relationship *relationshipdomain.State `json:"relationship,omitempty"`
	Posture      *personadomain.GroupPosture      `json:"posture,omitempty"`
	Ephemeral    *personadomain.EphemeralState    `json:"ephemeral,omitempty"`
}

// 存储接口定义

// SceneStore 群场景存储
type SceneStore interface {
	GetGroupScene(ctx context.Context, groupID int64) (*scenedomain.GroupScene, error)
	UpdateGroupScene(ctx context.Context, scene *scenedomain.GroupScene) error
}

// RelationshipStore 关系存储
type RelationshipStore interface {
	GetRelationship(ctx context.Context, personaID string, groupID, userID int64) (*relationshipdomain.State, error)
}

// PostureStore 群姿态存储
type PostureStore interface {
	GetGroupPosture(ctx context.Context, personaID string, groupID int64) (*personadomain.GroupPosture, error)
	UpdateGroupPosture(ctx context.Context, posture *personadomain.GroupPosture) error
}

// EphemeralStateStore 即时状态存储
type EphemeralStateStore interface {
	GetEphemeralState(ctx context.Context, personaID string, groupID int64) (*personadomain.EphemeralState, error)
	UpdateEphemeralState(ctx context.Context, state *personadomain.EphemeralState) error
}

// DefaultDecisionConfig 返回默认配置
func DefaultDecisionConfig() DecisionConfig {
	return DecisionConfig{
		CooldownSeconds:      10,
		ConsecutiveLimit:     3,
		EventExpirySeconds:   300,
		FastConversationGap:  5,
		MinTopicRelevance:    0.3,
		MinFamiliarity:       0.2,
		MaxFriction:          0.7,
		MinTrust:             0.3,
		MinEnergy:            0.3,
		MinSocialPatience:    0.2,
		MinConfidence:        0.6,
	}
}
