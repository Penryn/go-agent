package persona

import "time"

// PersonaContext 组装当前回合的完整人格上下文，分为三层：
// 1. 稳定身份（全局共享）
// 2. 群姿态（按群隔离）
// 3. 即时状态（按群隔离并自然衰减）
type PersonaContext struct {
	// 稳定身份层
	Identity PersonaConfig `json:"identity"`

	// 群姿态层
	Posture GroupPosture `json:"posture"`

	// 即时状态层
	EphemeralState EphemeralState `json:"ephemeral_state"`

	// 当前生效的事实集合（来自 PersonaFact）
	CanonicalFacts map[string]string `json:"canonical_facts"`
}

// GroupPosture 表示"我在这个群里是什么状态"，按群隔离，变化慢于即时状态。
type GroupPosture struct {
	PersonaID   string    `json:"persona_id"`
	GroupID     int64     `json:"group_id"`

	// Familiarity 熟悉度：0.0-1.0，影响是否主动接话
	Familiarity float64 `json:"familiarity"`

	// ParticipationBias 参与倾向：-1.0 到 1.0，负值更被动，正值更主动
	ParticipationBias float64 `json:"participation_bias"`

	// HumorLevel 幽默度：0.0-1.0，影响是否调侃和发梗图
	HumorLevel float64 `json:"humor_level"`

	// HelpfulnessBias 帮助倾向：0.0-1.0，影响是否提供实用信息
	HelpfulnessBias float64 `json:"helpfulness_bias"`

	// Formality 正式度：0.0-1.0，0 为随意，1 为正式
	Formality float64 `json:"formality"`

	// TrustInGroup 对群整体的信任度：0.0-1.0
	TrustInGroup float64 `json:"trust_in_group"`

	// PreferredTopics 偏好话题列表
	PreferredTopics []string `json:"preferred_topics"`

	// UpdatedAt 最后更新时间
	UpdatedAt time.Time `json:"updated_at"`

	// Revision 乐观锁版本
	Revision int64 `json:"revision"`
}

// EphemeralState 即时状态，按群隔离并自然衰减。
type EphemeralState struct {
	PersonaID string `json:"persona_id"`
	GroupID   int64  `json:"group_id"`

	// Mood 当前情绪
	Mood Mood `json:"mood"`

	// Energy 当前精力
	Energy Energy `json:"energy"`

	// SocialPatience 社交耐心：0.0-1.0，低于阈值时更倾向 silent
	SocialPatience float64 `json:"social_patience"`

	// LastTrigger 最近一次触发参与的原因
	LastTrigger string `json:"last_trigger,omitempty"`

	// UpdatedAt 最后更新时间
	UpdatedAt time.Time `json:"updated_at"`

	// ExpiresAt 过期时间，过期后恢复到基线状态
	ExpiresAt time.Time `json:"expires_at"`
}

// DefaultGroupPosture 返回新群的默认姿态
func DefaultGroupPosture(personaID string, groupID int64) GroupPosture {
	return GroupPosture{
		PersonaID:         personaID,
		GroupID:           groupID,
		Familiarity:       0.3,  // 初始略陌生
		ParticipationBias: 0.0,  // 中性
		HumorLevel:        0.5,  // 中等
		HelpfulnessBias:   0.6,  // 略倾向帮助
		Formality:         0.4,  // 略随意
		TrustInGroup:      0.5,  // 中性
		PreferredTopics:   []string{},
		UpdatedAt:         time.Now(),
		Revision:          1,
	}
}

// DefaultEphemeralState 返回基线即时状态
func DefaultEphemeralState(personaID string, groupID int64) EphemeralState {
	now := time.Now()
	return EphemeralState{
		PersonaID:      personaID,
		GroupID:        groupID,
		Mood:           MoodSteady,
		Energy:         EnergyNormal,
		SocialPatience: 0.8,
		UpdatedAt:      now,
		ExpiresAt:      now.Add(24 * time.Hour),
	}
}
