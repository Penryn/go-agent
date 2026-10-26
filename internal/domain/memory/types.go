package memory

import "time"

// SubjectKind 表示记忆主体的类型
type SubjectKind string

const (
	SubjectKindUser  SubjectKind = "user"
	SubjectKindGroup SubjectKind = "group"
	SubjectKindBot   SubjectKind = "bot"
)

// MemoryType 表示记忆的类型
type MemoryType string

const (
	MemoryTypeSemantic MemoryType = "semantic" // 语义记忆（事实、偏好）
	MemoryTypeSocial   MemoryType = "social"   // 社交记忆（互动规则）
	MemoryTypeEpisodic MemoryType = "episodic" // 情景记忆（共同经历）
)

// BotRole 表示 Bot 在情景记忆中的角色
type BotRole string

const (
	BotRoleParticipant BotRole = "participant" // 参与者（有成功发送）
	BotRoleObserver    BotRole = "observer"    // 旁观者（仅观察）
)

// Predicate 表示结构化事实的谓词
type Predicate string

const (
	PredicatePreferredName Predicate = "preferred_name" // 称呼偏好
	PredicateAllowPoke     Predicate = "allow_poke"     // 允许戳一戳
	PredicateAllowMention  Predicate = "allow_mention"  // 允许 @
	PredicateLikesTopic    Predicate = "likes_topic"    // 喜欢的话题（集合项）
)

// MemoryCandidate 表示待校验的记忆候选
type MemoryCandidate struct {
	// 身份
	Scope       string      `json:"scope"`
	SubjectKind SubjectKind `json:"subject_kind"`
	SubjectID   string      `json:"subject_id"`
	Type        MemoryType  `json:"type"`
	Subtype     string      `json:"subtype,omitempty"`

	// 内容
	Content         string    `json:"content"`
	Predicate       Predicate `json:"predicate,omitempty"`
	NormalizedValue string    `json:"normalized_value,omitempty"`
	Qualifier       string    `json:"qualifier,omitempty"`

	// 情景记忆特有
	ParticipantIDs []string `json:"participant_ids,omitempty"`
	BotRole        BotRole  `json:"bot_role,omitempty"`
	AnchorEventID  string   `json:"anchor_event_id,omitempty"`

	// 证据
	EvidenceEventIDs []string `json:"evidence_event_ids"`
	SourceRole       string   `json:"source_role"` // primary/context

	// 意图
	Intent       string     `json:"intent"`                  // new/update/supplement/correct/revoke
	SupersedesID string     `json:"supersedes_id,omitempty"` // 更新或替代的目标
	ValidUntil   *time.Time `json:"valid_until,omitempty"`

	// 提炼信息
	ExtractorVersion string    `json:"extractor_version"`
	ObservedAt       time.Time `json:"observed_at"`
}

// MemoryConstraint 表示行动前必须遵守的约束
type MemoryConstraint struct {
	SubjectKind SubjectKind `json:"subject_kind"`
	SubjectID   string      `json:"subject_id"`
	Type        string      `json:"type"` // preferred_name/allow_poke/allow_mention
	Value       string      `json:"value"`
	Qualifier   string      `json:"qualifier,omitempty"` // 时间区间等
	ValidUntil  *time.Time  `json:"valid_until,omitempty"`
}

// LearningEventProgress 表示学习任务的处理进度
type LearningEventProgress struct {
	EventID          string    `json:"event_id"`
	ExtractorVersion string    `json:"extractor_version"`
	GroupID          int64     `json:"group_id"`
	ProcessedAt      time.Time `json:"processed_at"`
	Outcome          string    `json:"outcome"`     // completed/skipped
	SkipReason       string    `json:"skip_reason"` // policy_excluded/already_forgotten/etc
	MemoryCount      int       `json:"memory_count"`
	CreatedAt        time.Time `json:"created_at"`
}
