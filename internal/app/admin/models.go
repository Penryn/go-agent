package admin

import (
	"time"

	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
)

// Snapshot 是管理后台的完整数据快照
type Snapshot struct {
	UpdatedAt     time.Time         `json:"updated_at"`
	SelectedGroup int64             `json:"selected_group"`
	Status        Status            `json:"status"`
	Stats         Stats             `json:"stats"`
	Persona       Persona           `json:"persona"`
	Groups        []Group           `json:"groups"`
	Memories      []Memory          `json:"memories"`
	Relationships []Relationship    `json:"relationships"`
	Activity      []Activity        `json:"activity"`
	Retrieval     RetrievalMetrics  `json:"retrieval"`
	ModelUsage    ModelUsageMetrics `json:"model_usage"`
	WindowMinutes int               `json:"window_minutes"`
	WindowMetrics WindowMetrics     `json:"window_metrics"`
}

type Status struct {
	Mode               string     `json:"mode"`
	QQEnabled          bool       `json:"qq_enabled"`
	QQConnected        bool       `json:"qq_connected"`
	SelfID             int64      `json:"self_id"`
	DatabaseOK         bool       `json:"database_ok"`
	QueueBacklog       int        `json:"queue_backlog"`
	LastErrorAt        *time.Time `json:"last_error_at,omitempty"`
	MainModelStatus    string     `json:"main_model_status"`
	VectorSearchStatus string     `json:"vector_search_status"`
	MainModelCheckedAt *time.Time `json:"main_model_checked_at,omitempty"`
	VectorCheckedAt    *time.Time `json:"vector_checked_at,omitempty"`
}

type Stats struct {
	Groups       int `json:"groups"`
	Members      int `json:"members"`
	Memories     int `json:"memories"`
	PendingTasks int `json:"pending_tasks"`
}

type Persona struct {
	ID          string                      `json:"id"`
	Name        string                      `json:"name"`
	Description string                      `json:"description"`
	Mood        string                      `json:"mood"`
	Energy      string                      `json:"energy"`
	TalkBias    float64                     `json:"talk_bias"`
	Runtime     policydomain.RuntimeState   `json:"runtime"`
	Facts       []personadomain.PersonaFact `json:"facts"`
	Interests   []string                    `json:"interests"`
}

type Group struct {
	GroupID      int64     `json:"group_id"`
	GroupName    string    `json:"group_name"`
	Messages     int       `json:"messages"`
	Members      int       `json:"members"`
	ActiveTopic  string    `json:"active_topic"`
	LastActivity time.Time `json:"last_activity"`
}

type Memory struct {
	ID            string     `json:"id"`
	Scope         string     `json:"scope"`
	Type          string     `json:"type"`
	Subject       string     `json:"subject"`
	Content       string     `json:"content"`
	Confidence    float64    `json:"confidence"`
	Importance    float64    `json:"importance"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	SourceEventID string     `json:"source_event_id"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type MemoryPage struct {
	Items    []Memory `json:"items"`
	Total    int      `json:"total"`
	Page     int      `json:"page"`
	PageSize int      `json:"page_size"`
}

type Meme struct {
	MemeID        string     `json:"meme_id"`
	GroupID       int64      `json:"group_id"`
	SourceEventID string     `json:"source_event_id"`
	ObjectKey     string     `json:"object_key"`
	FileExt       string     `json:"file_ext"`
	PreviewURL    string     `json:"preview_url"`
	Width         int        `json:"width"`
	Height        int        `json:"height"`
	Animated      bool       `json:"animated"`
	Status        string     `json:"status"`
	SendCount     int        `json:"send_count"`
	DudCount      int        `json:"dud_count"`
	CreatedAt     time.Time  `json:"created_at"`
	LastSentAt    *time.Time `json:"last_sent_at,omitempty"`
	Title         string     `json:"title"`
	Summary       string     `json:"summary"`
	Keywords      []string   `json:"keywords"`
	EmotionTags   []string   `json:"emotion_tags"`
	SceneTags     []string   `json:"scene_tags"`
	Confidence    float64    `json:"confidence"`
	Reviewed      bool       `json:"reviewed"`
}

type MemePage struct {
	Items    []Meme `json:"items"`
	Total    int    `json:"total"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
}

type Task struct {
	ID          string      `json:"id"`
	Kind        string      `json:"kind"`
	Status      string      `json:"status"`
	Attempts    int         `json:"attempts"`
	MaxAttempts int         `json:"max_attempts"`
	AvailableAt time.Time   `json:"available_at"`
	LastError   string      `json:"last_error"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
	Context     TaskContext `json:"context"`
}

type TaskContext struct {
	GroupID     int64    `json:"group_id,omitempty"`
	PayloadKeys []string `json:"payload_keys"`
}

type TaskPage struct {
	Items    []Task `json:"items"`
	Total    int    `json:"total"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
}

type Relationship struct {
	GroupID        int64     `json:"group_id"`
	UserID         int64     `json:"user_id"`
	Name           string    `json:"name"`
	Affinity       float64   `json:"affinity"`
	Familiarity    float64   `json:"familiarity"`
	TeaseTolerance float64   `json:"tease_tolerance"`
	Trust          float64   `json:"trust"`
	Friction       float64   `json:"friction"`
	MessageCount   int64     `json:"message_count"`
	LastInteractAt time.Time `json:"last_interact_at"`
}

type RelationshipPage struct {
	Items    []Relationship `json:"items"`
	Total    int            `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
}

type RelationshipEvent struct {
	EventID         string    `json:"event_id"`
	Kind            string    `json:"kind"`
	Valence         float64   `json:"valence"`
	EvidenceEventID string    `json:"evidence_event_id,omitempty"`
	DecisionID      string    `json:"decision_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type ProjectionSnapshot struct {
	Familiarity    float64   `json:"familiarity"`
	Affinity       float64   `json:"affinity"`
	Trust          float64   `json:"trust"`
	TeaseTolerance float64   `json:"tease_tolerance"`
	Friction       float64   `json:"friction"`
	Revision       int64     `json:"revision"`
	TriggerEventID string    `json:"trigger_event_id,omitempty"`
	TriggerKind    string    `json:"trigger_kind,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Activity struct {
	EventID string    `json:"event_id"`
	At      time.Time `json:"at"`
	GroupID int64     `json:"group_id"`
	Type    string    `json:"type"`
	Label   string    `json:"label"`
	Subject string    `json:"subject"`
	Detail  string    `json:"detail"`
}

type ActivityPage struct {
	Items         []Activity `json:"items"`
	Total         int        `json:"total"`
	MessageCount  int        `json:"message_count"`
	DecisionCount int        `json:"decision_count"`
	Page          int        `json:"page"`
	PageSize      int        `json:"page_size"`
}

type EventDetail struct {
	EventID     string             `json:"event_id"`
	MessageID   string             `json:"message_id"`
	GroupID     int64              `json:"group_id"`
	UserID      int64              `json:"user_id"`
	Kind        string             `json:"kind"`
	Text        string             `json:"text"`
	Sender      string             `json:"sender"`
	OccurredAt  time.Time          `json:"occurred_at"`
	DurationMS  int64              `json:"duration_ms"`
	Decision    *DecisionDetail    `json:"decision,omitempty"`
	Retrievals  []RetrievalDetail  `json:"retrievals"`
	ModelUsages []ModelUsageDetail `json:"model_usages"`
}

type DecisionDetail struct {
	ThoughtID      string    `json:"thought_id"`
	Action         string    `json:"action"`
	Outcome        string    `json:"outcome"`
	Interpretation string    `json:"interpretation"`
	Evidence       []string  `json:"evidence"`
	Uncertainty    float64   `json:"uncertainty"`
	CreatedAt      time.Time `json:"created_at"`
}

type RetrievalDetail struct {
	TraceID         string             `json:"trace_id"`
	Query           string             `json:"query"`
	CandidateCount  int                `json:"candidate_count"`
	HitMemoryIDs    []string           `json:"hit_memory_ids"`
	SelectedIDs     []string           `json:"selected_ids"`
	Outcome         string             `json:"outcome"`
	LexicalRanks    map[string]int     `json:"lexical_ranks"`
	VectorRanks     map[string]int     `json:"vector_ranks"`
	CandidateScores map[string]float64 `json:"candidate_scores"`
	LatencyMS       int64              `json:"latency_ms"`
	DegradedTracks  []string           `json:"degraded_tracks"`
	SelectionReason string             `json:"selection_reason"`
	CreatedAt       time.Time          `json:"created_at"`
}

type ModelUsageDetail struct {
	TraceID         string            `json:"trace_id"`
	Iteration       int               `json:"iteration"`
	InputTokens     int               `json:"input_tokens"`
	CachedTokens    int               `json:"cached_tokens"`
	CacheMissTokens int               `json:"cache_miss_tokens"`
	OutputTokens    int               `json:"output_tokens"`
	DurationMS      int64             `json:"duration_ms"`
	PromptShape     PromptShapeDetail `json:"prompt_shape"`
	Tools           []string          `json:"tools"`
	ToolCalls       []ToolCallDetail  `json:"tool_calls"`
	UsageAvailable  bool              `json:"usage_available"`
	Error           string            `json:"error"`
	Sent            bool              `json:"sent"`
	FinalAction     string            `json:"final_action"`
	DropReason      string            `json:"drop_reason"`
	CreatedAt       time.Time         `json:"created_at"`
}

type PromptShapeDetail struct {
	StaticBytes      int `json:"static_bytes"`
	SessionBytes     int `json:"session_bytes"`
	HistoryBytes     int `json:"history_bytes"`
	CurrentTurnBytes int `json:"current_turn_bytes"`
	MemoryBytes      int `json:"memory_bytes"`
	ToolSchemaBytes  int `json:"tool_schema_bytes"`
	MessageCount     int `json:"message_count"`
	ToolCount        int `json:"tool_count"`
}

type ToolCallDetail struct {
	Name       string `json:"name"`
	Arguments  string `json:"arguments"`
	Result     string `json:"result"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error"`
}

type RetrievalMetrics struct {
	Queries               int     `json:"queries"`
	QueriesWithHits       int     `json:"queries_with_hits"`
	HitRate               float64 `json:"hit_rate"`
	AvgCandidateCount     float64 `json:"avg_candidate_count"`
	ResultRecordedQueries int     `json:"result_recorded_queries"`
	SelectedQueries       int     `json:"selected_queries"`
	SelectionRate         float64 `json:"selection_rate"`
}

type ModelUsageMetrics struct {
	Calls           int     `json:"calls"`
	InputTokens     int64   `json:"input_tokens"`
	CachedTokens    int64   `json:"cached_tokens"`
	CacheMissTokens int64   `json:"cache_miss_tokens"`
	UncachedTokens  int64   `json:"uncached_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	AvgDurationMS   float64 `json:"avg_duration_ms"`
	ErrorCalls      int     `json:"error_calls"`
}

type WindowMetrics struct {
	Decisions       int `json:"decisions"`
	ActionDecisions int `json:"action_decisions"`
	Replies         int `json:"replies"`
	Tasks           int `json:"tasks"`
	FailedTasks     int `json:"failed_tasks"`
}

type MetricPoint struct {
	At              time.Time `json:"at"`
	Queries         int       `json:"queries"`
	QueriesWithHits int       `json:"queries_with_hits"`
	SelectedQueries int       `json:"selected_queries"`
	Decisions       int       `json:"decisions"`
	Replies         int       `json:"replies"`
	ModelCalls      int       `json:"model_calls"`
	ModelErrors     int       `json:"model_errors"`
	AvgDurationMS   float64   `json:"avg_duration_ms"`
}

type MetricSeries struct {
	Points []MetricPoint `json:"points"`
}
