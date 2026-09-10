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

// MemoryStatus 表示记忆的状态
type MemoryStatus string

const (
	MemoryStatusActive     MemoryStatus = "active"     // 当前有效
	MemoryStatusPending    MemoryStatus = "pending"    // 等待补充证据
	MemoryStatusSuperseded MemoryStatus = "superseded" // 被新版本替代
	MemoryStatusExpired    MemoryStatus = "expired"    // 时间失效
	MemoryStatusRevoked    MemoryStatus = "revoked"    // 明确遗忘
	MemoryStatusRejected   MemoryStatus = "rejected"   // 否决
)

// SourceKind 表示记忆的来源
type SourceKind string

const (
	SourceKindExtraction SourceKind = "extraction" // 自动提炼
	SourceKindCorrection SourceKind = "correction" // 用户更正
	SourceKindManual     SourceKind = "manual"     // 手动录入
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

// Memory 表示一条权威记忆
type Memory struct {
	// 身份
	MemoryID    string      `json:"memory_id"`
	Scope       string      `json:"scope"`        // 可见范围（通常是群ID）
	SubjectKind SubjectKind `json:"subject_kind"` // 主体类型
	SubjectID   string      `json:"subject_id"`   // 主体ID
	Type        MemoryType  `json:"type"`
	Subtype     string      `json:"subtype"`

	// 内容
	Content         string    `json:"content"`           // 自然语言描述
	Predicate       Predicate `json:"predicate"`         // 结构化事实键
	NormalizedValue string    `json:"normalized_value"`  // 规范化值
	Qualifier       string    `json:"qualifier"`         // 限定符（default 或时间区间）

	// 情景记忆特有
	ParticipantIDs []string `json:"participant_ids"` // 参与者用户ID列表
	BotRole        BotRole  `json:"bot_role"`        // Bot 角色
	AnchorEventID  string   `json:"anchor_event_id"` // 原事件锚点

	// 状态
	Status       MemoryStatus `json:"status"`
	Revision     int64        `json:"revision"`
	SupersedesID string       `json:"supersedes_id"` // 替代的旧记忆ID

	// 时间
	FirstObservedAt time.Time  `json:"first_observed_at"`
	LastObservedAt  time.Time  `json:"last_observed_at"`
	ValidUntil      *time.Time `json:"valid_until,omitempty"`   // 临时边界失效
	PendingUntil    *time.Time `json:"pending_until,omitempty"` // pending 到期

	// 来源
	SourceKind        SourceKind `json:"source_kind"`
	ExtractorVersion  string     `json:"extractor_version"`

	// 元数据
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Evidence 表示记忆的证据
type Evidence struct {
	MemoryID   string    `json:"memory_id"`
	EventID    string    `json:"event_id"`
	SourceRole string    `json:"source_role"` // primary/context/reference
	AddedAt    time.Time `json:"added_at"`
}

// Change 表示记忆的变更记录
type Change struct {
	ChangeID     string            `json:"change_id"`
	MemoryID     string            `json:"memory_id"`
	ChangeKind   string            `json:"change_kind"` // created/updated/superseded/expired/revoked
	Reason       string            `json:"reason"`
	OperatorKind string            `json:"operator_kind"` // system/user/correction
	OperatorID   string            `json:"operator_id"`
	FromRevision int64             `json:"from_revision"`
	ToRevision   int64             `json:"to_revision"`
	FieldDiffs   map[string]string `json:"field_diffs"`
	CreatedAt    time.Time         `json:"created_at"`
}

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
	Intent       string     `json:"intent"`        // new/update/supplement/correct/revoke
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

// MemoryContext 表示本轮读取的记忆上下文
type MemoryContext struct {
	Constraints       []MemoryConstraint `json:"constraints"`
	RelevantMemories  []MemoryWithSource `json:"relevant_memories"`
	RetrievalTraceID  string             `json:"retrieval_trace_id,omitempty"`
}

// MemoryWithSource 表示带来源信息的记忆
type MemoryWithSource struct {
	Memory
	EvidenceCount    int       `json:"evidence_count"`
	EvidenceEventIDs []string  `json:"evidence_event_ids"`
	SourceSummary    string    `json:"source_summary"` // "张三在2024-01-15说过"
}

// LearningEventProgress 表示学习任务的处理进度
type LearningEventProgress struct {
	EventID          string    `json:"event_id"`
	ExtractorVersion string    `json:"extractor_version"`
	GroupID          int64     `json:"group_id"`
	ProcessedAt      time.Time `json:"processed_at"`
	Outcome          string    `json:"outcome"`      // completed/skipped
	SkipReason       string    `json:"skip_reason"`  // policy_excluded/already_forgotten/etc
	MemoryCount      int       `json:"memory_count"`
	CreatedAt        time.Time `json:"created_at"`
}
