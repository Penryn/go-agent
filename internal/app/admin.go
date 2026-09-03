package app

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	napcatsdk "github.com/zjutjh/napcat-sdk"
	"github.com/zjutjh/napcat-sdk/api"

	"github.com/phlin/go-agent/internal/application/ports"
	toolsvc "github.com/phlin/go-agent/internal/application/tools"
	"github.com/phlin/go-agent/internal/config"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
)

type adminDashboard struct {
	db                *sql.DB
	state             ports.RuntimeStateStore
	facts             ports.PersonaFactStore
	definition        personadomain.PersonaDefinition
	cfg               config.Config
	connected         func() bool
	mainModelReady    bool
	vectorSearchReady bool
	health            *capabilityHealth
	groupClient       *napcatsdk.Client
	groupNamesMu      sync.Mutex
	groupNames        map[int64]string
	groupNamesAt      time.Time
}

type adminSnapshot struct {
	UpdatedAt     time.Time              `json:"updated_at"`
	SelectedGroup int64                  `json:"selected_group"`
	Status        adminStatus            `json:"status"`
	Stats         adminStats             `json:"stats"`
	Persona       adminPersona           `json:"persona"`
	Groups        []adminGroup           `json:"groups"`
	Memories      []adminMemory          `json:"memories"`
	Relationships []adminRelationship    `json:"relationships"`
	Activity      []adminActivity        `json:"activity"`
	Retrieval     adminRetrievalMetrics  `json:"retrieval"`
	ModelUsage    adminModelUsageMetrics `json:"model_usage"`
	WindowMinutes int                    `json:"window_minutes"`
	WindowMetrics adminWindowMetrics     `json:"window_metrics"`
}

type adminStatus struct {
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

type adminStats struct {
	Groups       int `json:"groups"`
	Members      int `json:"members"`
	Memories     int `json:"memories"`
	PendingTasks int `json:"pending_tasks"`
}

type adminPersona struct {
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

type adminGroup struct {
	GroupID      int64     `json:"group_id"`
	GroupName    string    `json:"group_name"`
	Messages     int       `json:"messages"`
	Members      int       `json:"members"`
	ActiveTopic  string    `json:"active_topic"`
	LastActivity time.Time `json:"last_activity"`
}

type adminMemory struct {
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

type adminMemoryPage struct {
	Items    []adminMemory `json:"items"`
	Total    int           `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
}

type adminMeme struct {
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

type adminMemePage struct {
	Items    []adminMeme `json:"items"`
	Total    int         `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}

type adminTask struct {
	ID          string           `json:"id"`
	Kind        string           `json:"kind"`
	Status      string           `json:"status"`
	Attempts    int              `json:"attempts"`
	MaxAttempts int              `json:"max_attempts"`
	AvailableAt time.Time        `json:"available_at"`
	LastError   string           `json:"last_error"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
	Context     adminTaskContext `json:"context"`
}

type adminTaskContext struct {
	GroupID     int64    `json:"group_id,omitempty"`
	PayloadKeys []string `json:"payload_keys"`
}

type adminTaskPage struct {
	Items    []adminTask `json:"items"`
	Total    int         `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}

type adminRelationship struct {
	GroupID        int64     `json:"group_id"`
	UserID         int64     `json:"user_id"`
	Name           string    `json:"name"`
	Affinity       float64   `json:"affinity"`
	Familiarity    float64   `json:"familiarity"`
	TeaseTolerance float64   `json:"tease_tolerance"`
	GrudgeScore    float64   `json:"grudge_score"`
	MessageCount   int64     `json:"message_count"`
	LastInteractAt time.Time `json:"last_interact_at"`
}

type adminRelationshipPage struct {
	Items    []adminRelationship `json:"items"`
	Total    int                 `json:"total"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"page_size"`
}

type adminActivity struct {
	EventID string    `json:"event_id"`
	At      time.Time `json:"at"`
	GroupID int64     `json:"group_id"`
	Type    string    `json:"type"`
	Label   string    `json:"label"`
	Subject string    `json:"subject"`
	Detail  string    `json:"detail"`
}

type adminActivityPage struct {
	Items         []adminActivity `json:"items"`
	Total         int             `json:"total"`
	MessageCount  int             `json:"message_count"`
	DecisionCount int             `json:"decision_count"`
	Page          int             `json:"page"`
	PageSize      int             `json:"page_size"`
}

type adminEventDetail struct {
	EventID     string                  `json:"event_id"`
	MessageID   string                  `json:"message_id"`
	GroupID     int64                   `json:"group_id"`
	UserID      int64                   `json:"user_id"`
	Kind        string                  `json:"kind"`
	Text        string                  `json:"text"`
	Sender      string                  `json:"sender"`
	OccurredAt  time.Time               `json:"occurred_at"`
	DurationMS  int64                   `json:"duration_ms"`
	Decision    *adminDecisionDetail    `json:"decision,omitempty"`
	Retrievals  []adminRetrievalDetail  `json:"retrievals"`
	ModelUsages []adminModelUsageDetail `json:"model_usages"`
}

type adminDecisionDetail struct {
	ThoughtID      string    `json:"thought_id"`
	Action         string    `json:"action"`
	Outcome        string    `json:"outcome"`
	Interpretation string    `json:"interpretation"`
	Evidence       []string  `json:"evidence"`
	Uncertainty    float64   `json:"uncertainty"`
	CreatedAt      time.Time `json:"created_at"`
}

type adminRetrievalDetail struct {
	TraceID        string    `json:"trace_id"`
	Query          string    `json:"query"`
	CandidateCount int       `json:"candidate_count"`
	HitMemoryIDs   []string  `json:"hit_memory_ids"`
	SelectedIDs    []string  `json:"selected_ids"`
	Outcome        string    `json:"outcome"`
	CreatedAt      time.Time `json:"created_at"`
}

type adminModelUsageDetail struct {
	TraceID        string                `json:"trace_id"`
	Iteration      int                   `json:"iteration"`
	InputTokens    int                   `json:"input_tokens"`
	OutputTokens   int                   `json:"output_tokens"`
	DurationMS     int64                 `json:"duration_ms"`
	Tools          []string              `json:"tools"`
	ToolCalls      []adminToolCallDetail `json:"tool_calls"`
	UsageAvailable bool                  `json:"usage_available"`
	Error          string                `json:"error"`
	Sent           bool                  `json:"sent"`
	FinalAction    string                `json:"final_action"`
	DropReason     string                `json:"drop_reason"`
	CreatedAt      time.Time             `json:"created_at"`
}

type adminToolCallDetail struct {
	Name       string `json:"name"`
	Arguments  string `json:"arguments"`
	Result     string `json:"result"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error"`
}

type adminRetrievalMetrics struct {
	Queries               int     `json:"queries"`
	QueriesWithHits       int     `json:"queries_with_hits"`
	HitRate               float64 `json:"hit_rate"`
	AvgCandidateCount     float64 `json:"avg_candidate_count"`
	ResultRecordedQueries int     `json:"result_recorded_queries"`
	SelectedQueries       int     `json:"selected_queries"`
	SelectionRate         float64 `json:"selection_rate"`
}

type adminModelUsageMetrics struct {
	Calls         int     `json:"calls"`
	InputTokens   int64   `json:"input_tokens"`
	OutputTokens  int64   `json:"output_tokens"`
	AvgDurationMS float64 `json:"avg_duration_ms"`
	ErrorCalls    int     `json:"error_calls"`
}

type adminWindowMetrics struct {
	Decisions       int `json:"decisions"`
	ActionDecisions int `json:"action_decisions"`
	Replies         int `json:"replies"`
	Tasks           int `json:"tasks"`
	FailedTasks     int `json:"failed_tasks"`
}

type adminMetricPoint struct {
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

type adminMetricSeries struct {
	Points []adminMetricPoint `json:"points"`
}

func newAdminHandler(db *sql.DB, state ports.RuntimeStateStore, facts ports.PersonaFactStore, definition personadomain.PersonaDefinition, cfg config.Config, connected func() bool, mcp *toolsvc.MCPManager, mainModelReady, vectorSearchReady bool, health *capabilityHealth) http.Handler {
	dashboard := &adminDashboard{db: db, state: state, facts: facts, definition: definition, cfg: cfg, connected: connected, mainModelReady: mainModelReady, vectorSearchReady: vectorSearchReady, health: health}
	if strings.TrimSpace(cfg.QQ.OutboundURL) != "" {
		dashboard.groupClient = napcatsdk.NewHTTPClient(cfg.QQ.OutboundURL,
			napcatsdk.WithToken(cfg.QQ.OutboundToken), napcatsdk.WithHTTPTimeout(2*time.Second))
	}
	assets, _ := fs.Sub(adminAssets, "adminui/dist")
	return &adminHandler{
		token:           strings.TrimSpace(cfg.Server.AdminToken),
		personaID:       definition.Config.ID,
		load:            dashboard.snapshot,
		loadWindow:      dashboard.snapshotWindow,
		loadCore:        dashboard.snapshotCore,
		mcp:             mcp,
		db:              db,
		memeStoragePath: cfg.Meme.StoragePath,
		assets:          http.StripPrefix("/admin/", http.FileServer(http.FS(assets))),
	}
}

type adminHandler struct {
	token           string
	personaID       string
	load            func(context.Context, int64) (adminSnapshot, error)
	loadWindow      func(context.Context, int64, int) (adminSnapshot, error)
	loadCore        func(context.Context, int64, int) (adminSnapshot, error)
	assets          http.Handler
	db              *sql.DB
	mcp             *toolsvc.MCPManager
	memeStoragePath string
}

//go:embed adminui/dist
var adminAssets embed.FS

func (h *adminHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/admin/api/memes/files/") {
		h.handleMemeFile(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/admin/api/events/") {
		h.handleEventDetail(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/admin/api/memes/") {
		h.handleMeme(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/admin/api/tasks/") {
		h.handleTask(w, r)
		return
	}
	switch r.URL.Path {
	case "/admin":
		http.Redirect(w, r, "/admin/", http.StatusTemporaryRedirect)
	case "/admin/api/snapshot":
		h.handleSnapshot(w, r)
	case "/admin/api/mcp":
		h.handleMCP(w, r)
	case "/admin/api/memes":
		h.handleMemes(w, r)
	case "/admin/api/relationships":
		h.handleRelationships(w, r)
	case "/admin/api/tasks":
		h.handleTasks(w, r)
	case "/admin/api/activity":
		h.handleActivity(w, r)
	case "/admin/api/metrics":
		h.handleMetrics(w, r)
	case "/admin/api/memories":
		h.handleMemories(w, r)
	default:
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/admin/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; img-src 'self' data: https: http:")
		h.assets.ServeHTTP(w, r)
	}
}

func (h *adminHandler) handleMemeFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r) && !h.authorizedQueryToken(r) {
		http.Error(w, "admin token required", http.StatusUnauthorized)
		return
	}
	name, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/admin/api/memes/files/"))
	if err != nil || name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\\`) {
		http.Error(w, "invalid meme file", http.StatusBadRequest)
		return
	}
	base, err := filepath.Abs(strings.TrimSpace(h.memeStoragePath))
	if err != nil || strings.TrimSpace(h.memeStoragePath) == "" {
		http.NotFound(w, r)
		return
	}
	target := filepath.Join(base, name)
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil || filepath.Dir(resolvedTarget) != resolvedBase {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(resolvedTarget)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	if contentType := mime.TypeByExtension(filepath.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	http.ServeFile(w, r, resolvedTarget)
}

func (h *adminHandler) authorizedQueryToken(r *http.Request) bool {
	if h.token == "" {
		return false
	}
	provided := strings.TrimSpace(r.URL.Query().Get("token"))
	return len(provided) == len(h.token) && subtle.ConstantTimeCompare([]byte(provided), []byte(h.token)) == 1
}

func (h *adminHandler) handleActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r) {
		http.Error(w, "admin token required", http.StatusUnauthorized)
		return
	}
	groupID := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("group_id")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			http.Error(w, "invalid group_id", http.StatusBadRequest)
			return
		}
		groupID = parsed
	}
	windowMinutes := 1440
	if raw := strings.TrimSpace(r.URL.Query().Get("window_minutes")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || !slices.Contains([]int{10, 60, 1440}, parsed) {
			http.Error(w, "invalid window_minutes", http.StatusBadRequest)
			return
		}
		windowMinutes = parsed
	}
	activityType := strings.TrimSpace(r.URL.Query().Get("type"))
	if activityType != "" && !slices.Contains([]string{"message", "decision"}, activityType) {
		http.Error(w, "invalid type", http.StatusBadRequest)
		return
	}
	page, pageSize, err := parseAdminPage(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	result, err := loadAdminActivityPage(ctx, h.db, groupID, windowMinutes, activityType, page, pageSize)
	if err != nil {
		http.Error(w, "load activity: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, result)
}

func (h *adminHandler) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r) {
		http.Error(w, "admin token required", http.StatusUnauthorized)
		return
	}
	groupID := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("group_id")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			http.Error(w, "invalid group_id", http.StatusBadRequest)
			return
		}
		groupID = parsed
	}
	windowMinutes := 1440
	if raw := strings.TrimSpace(r.URL.Query().Get("window_minutes")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || !slices.Contains([]int{10, 60, 1440}, parsed) {
			http.Error(w, "invalid window_minutes", http.StatusBadRequest)
			return
		}
		windowMinutes = parsed
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	series, err := loadAdminMetricSeries(ctx, h.db, groupID, windowMinutes)
	if err != nil {
		http.Error(w, "load metrics: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, series)
}

func parseAdminPage(r *http.Request) (int, int, error) {
	page, pageSize := 1, 50
	if raw := strings.TrimSpace(r.URL.Query().Get("page")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100000 {
			return 0, 0, errors.New("invalid page")
		}
		page = parsed
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("page_size")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			return 0, 0, errors.New("invalid page_size")
		}
		pageSize = parsed
	}
	return page, pageSize, nil
}

func (h *adminHandler) handleMemories(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r) {
		http.Error(w, "admin token required", http.StatusUnauthorized)
		return
	}
	groupID := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("group_id")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			http.Error(w, "invalid group_id", http.StatusBadRequest)
			return
		}
		groupID = parsed
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "active"
	}
	if !slices.Contains([]string{"active", "expired", "all"}, status) {
		http.Error(w, "invalid status", http.StatusBadRequest)
		return
	}
	page, pageSize, err := parseAdminPage(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	memories, err := loadAdminMemoryPage(ctx, h.db, groupID, status, strings.TrimSpace(r.URL.Query().Get("type")), r.URL.Query().Get("q"), page, pageSize)
	if err != nil {
		http.Error(w, "load memories: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, memories)
}

func (h *adminHandler) handleMeme(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.Header().Set("Allow", http.MethodDelete)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r) {
		http.Error(w, "admin token required", http.StatusUnauthorized)
		return
	}
	memeID, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/admin/api/memes/"))
	if err != nil || memeID == "" || strings.Contains(memeID, "/") {
		http.Error(w, "invalid meme_id", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		http.Error(w, "delete meme: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	var objectKey string
	if err := tx.QueryRowContext(ctx, `SELECT object_key FROM meme_assets WHERE meme_id = $1`, memeID).Scan(&objectKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "delete meme: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM meme_descriptors WHERE meme_id = $1`, memeID); err != nil {
		http.Error(w, "delete meme descriptor: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM meme_vectors WHERE meme_id = $1`, memeID); err != nil {
		http.Error(w, "delete meme vector: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM async_outbox WHERE kind = 'meme_vector_index' AND payload_json->>'meme_id' = $1`, memeID); err != nil {
		http.Error(w, "delete meme tasks: "+err.Error(), http.StatusInternalServerError)
		return
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM meme_assets WHERE meme_id = $1`, memeID)
	if err != nil {
		http.Error(w, "delete meme asset: "+err.Error(), http.StatusInternalServerError)
		return
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		http.Error(w, "delete meme: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if deleted == 0 {
		http.NotFound(w, r)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "delete meme: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if filePath, err := h.memeFilePath(objectKey); err == nil {
		if err := os.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("admin: remove deleted meme file failed", "path", filePath, "err", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *adminHandler) memeFilePath(name string) (string, error) {
	base, err := filepath.Abs(strings.TrimSpace(h.memeStoragePath))
	if err != nil || strings.TrimSpace(h.memeStoragePath) == "" {
		return "", fmt.Errorf("meme storage path is not configured")
	}
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\\`) {
		return "", fmt.Errorf("invalid meme file")
	}
	return filepath.Join(base, name), nil
}

func (h *adminHandler) handleMemes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r) {
		http.Error(w, "admin token required", http.StatusUnauthorized)
		return
	}
	groupID := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("group_id")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			http.Error(w, "invalid group_id", http.StatusBadRequest)
			return
		}
		groupID = parsed
	}
	page, pageSize, err := parseAdminPage(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	memes, err := loadAdminMemePage(ctx, h.db, groupID, r.URL.Query().Get("q"), page, pageSize, h.memeStoragePath)
	if err != nil {
		http.Error(w, "load memes: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, memes)
}

func (h *adminHandler) handleRelationships(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r) {
		http.Error(w, "admin token required", http.StatusUnauthorized)
		return
	}
	groupID := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("group_id")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			http.Error(w, "invalid group_id", http.StatusBadRequest)
			return
		}
		groupID = parsed
	}
	page, pageSize, err := parseAdminPage(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	relationships, err := loadAdminRelationshipPage(ctx, h.db, h.personaID, groupID, r.URL.Query().Get("q"), page, pageSize)
	if err != nil {
		http.Error(w, "load relationships: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, relationships)
}

func (h *adminHandler) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r) {
		http.Error(w, "admin token required", http.StatusUnauthorized)
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && !slices.Contains([]string{"pending", "running", "retry", "completed", "dead_letter"}, status) {
		http.Error(w, "invalid status", http.StatusBadRequest)
		return
	}
	page := 1
	if raw := strings.TrimSpace(r.URL.Query().Get("page")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100000 {
			http.Error(w, "invalid page", http.StatusBadRequest)
			return
		}
		page = parsed
	}
	pageSize := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("page_size")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			http.Error(w, "invalid page_size", http.StatusBadRequest)
			return
		}
		pageSize = parsed
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	where := ""
	args := []any{}
	if status != "" {
		where = " WHERE status = $1"
		args = append(args, status)
	}
	var total int
	if err := h.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM async_outbox"+where, args...).Scan(&total); err != nil {
		http.Error(w, "count tasks: "+err.Error(), http.StatusInternalServerError)
		return
	}
	offset := (page - 1) * pageSize
	query := "SELECT task_id, kind, status, attempts, max_attempts, available_at, COALESCE(last_error, ''), created_at, updated_at, payload_json FROM async_outbox" + where + fmt.Sprintf(" ORDER BY updated_at DESC LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	args = append(args, pageSize, offset)
	rows, err := h.db.QueryContext(ctx, query, args...)
	if err != nil {
		http.Error(w, "load tasks: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	tasks := []adminTask{}
	for rows.Next() {
		var task adminTask
		var payload json.RawMessage
		if err := rows.Scan(&task.ID, &task.Kind, &task.Status, &task.Attempts, &task.MaxAttempts, &task.AvailableAt, &task.LastError, &task.CreatedAt, &task.UpdatedAt, &payload); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		task.Context = summarizeTaskPayload(payload)
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, adminTaskPage{Items: tasks, Total: total, Page: page, PageSize: pageSize})
}

func summarizeTaskPayload(payload []byte) adminTaskContext {
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil {
		return adminTaskContext{PayloadKeys: []string{}}
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	context := adminTaskContext{PayloadKeys: keys}
	if raw, ok := fields["group_id"]; ok {
		var groupID int64
		if json.Unmarshal(raw, &groupID) == nil && groupID > 0 {
			context.GroupID = groupID
		}
	}
	return context
}

func (h *adminHandler) handleTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r) {
		http.Error(w, "admin token required", http.StatusUnauthorized)
		return
	}
	rawTaskPath := strings.TrimPrefix(r.URL.Path, "/admin/api/tasks/")
	if !strings.HasSuffix(rawTaskPath, "/retry") {
		http.NotFound(w, r)
		return
	}
	taskID, err := url.PathUnescape(strings.TrimSuffix(rawTaskPath, "/retry"))
	if err != nil || taskID == "" || strings.Contains(taskID, "/") {
		http.Error(w, "invalid task_id", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	result, err := h.db.ExecContext(ctx, `
		UPDATE async_outbox
		SET status = 'pending', attempts = 0, available_at = NOW(), locked_until = NULL,
		    locked_by = NULL, last_error = NULL, updated_at = NOW()
		WHERE task_id = $1 AND status = 'dead_letter'
	`, taskID)
	if err != nil {
		http.Error(w, "retry task: "+err.Error(), http.StatusInternalServerError)
		return
	}
	affected, err := result.RowsAffected()
	if err != nil {
		http.Error(w, "retry task: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if affected == 0 {
		http.Error(w, "task is not a dead letter or does not exist", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *adminHandler) handleEventDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r) {
		http.Error(w, "admin token required", http.StatusUnauthorized)
		return
	}
	eventID, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/admin/api/events/"))
	if err != nil || eventID == "" {
		http.Error(w, "invalid event_id", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	detail, err := loadAdminEventDetail(ctx, h.db, eventID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "load event detail: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, detail)
}

type adminMCPConfig struct {
	Servers []config.MCPServerConfig `json:"servers"`
	Tools   []toolsvc.MCPToolInfo    `json:"tools"`
}

func (h *adminHandler) handleMCP(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		http.Error(w, "admin token required", http.StatusUnauthorized)
		return
	}
	if h.mcp == nil || h.db == nil {
		http.Error(w, "MCP manager unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		tools, err := h.mcp.ToolInfos(ctx)
		if err != nil {
			http.Error(w, "load MCP tools: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, adminMCPConfig{Servers: h.mcp.Servers(), Tools: tools})
	case http.MethodPut:
		var payload adminMCPConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&payload); err != nil {
			http.Error(w, "invalid MCP config: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := config.ValidateMCPServers(payload.Servers); err != nil {
			http.Error(w, "invalid MCP config: "+err.Error(), http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		previous := h.mcp.Servers()
		if err := h.mcp.Apply(ctx, payload.Servers); err != nil {
			http.Error(w, "apply MCP config: "+err.Error(), http.StatusBadGateway)
			return
		}
		if err := saveRuntimeMCPConfig(ctx, h.db, payload.Servers); err != nil {
			_ = h.mcp.Apply(context.Background(), previous)
			http.Error(w, "persist MCP config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		tools, err := h.mcp.ToolInfos(ctx)
		if err != nil {
			http.Error(w, "load MCP tools: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, adminMCPConfig{Servers: h.mcp.Servers(), Tools: tools})
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}

func (h *adminHandler) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="bot-admin"`)
		http.Error(w, "admin token required", http.StatusUnauthorized)
		return
	}
	groupID, err := strconv.ParseInt(r.URL.Query().Get("group_id"), 10, 64)
	if r.URL.Query().Get("group_id") != "" && (err != nil || groupID < 0) {
		http.Error(w, "invalid group_id", http.StatusBadRequest)
		return
	}
	window := 1440
	if raw := r.URL.Query().Get("window_minutes"); raw != "" {
		window, err = strconv.Atoi(raw)
		if err != nil || !slices.Contains([]int{10, 60, 1440}, window) {
			http.Error(w, "invalid window_minutes", http.StatusBadRequest)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	var snapshot adminSnapshot
	if r.URL.Query().Get("mode") == "core" && h.loadCore != nil {
		snapshot, err = h.loadCore(ctx, groupID, window)
	} else if h.loadWindow != nil {
		snapshot, err = h.loadWindow(ctx, groupID, window)
	} else {
		snapshot, err = h.load(ctx, groupID)
	}
	if err != nil {
		http.Error(w, "load dashboard: "+err.Error(), http.StatusInternalServerError)
		return
	}
	normalizeAdminSnapshot(&snapshot)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(snapshot)
}

func normalizeAdminSnapshot(snapshot *adminSnapshot) {
	if snapshot.Groups == nil {
		snapshot.Groups = []adminGroup{}
	}
	if snapshot.Memories == nil {
		snapshot.Memories = []adminMemory{}
	}
	if snapshot.Relationships == nil {
		snapshot.Relationships = []adminRelationship{}
	}
	if snapshot.Activity == nil {
		snapshot.Activity = []adminActivity{}
	}
	if snapshot.Persona.Facts == nil {
		snapshot.Persona.Facts = []personadomain.PersonaFact{}
	}
	if snapshot.Persona.Interests == nil {
		snapshot.Persona.Interests = []string{}
	}
}

func (h *adminHandler) authorized(r *http.Request) bool {
	if h.token == "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		return net.ParseIP(host).IsLoopback()
	}
	provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	return len(provided) == len(h.token) && subtle.ConstantTimeCompare([]byte(provided), []byte(h.token)) == 1
}

func (d *adminDashboard) snapshot(ctx context.Context, selectedGroup int64) (adminSnapshot, error) {
	return d.snapshotWindow(ctx, selectedGroup, 1440)
}

func (d *adminDashboard) snapshotWindow(ctx context.Context, selectedGroup int64, windowMinutes int) (adminSnapshot, error) {
	return d.snapshotWithDetail(ctx, selectedGroup, windowMinutes, true)
}

func (d *adminDashboard) snapshotCore(ctx context.Context, selectedGroup int64, windowMinutes int) (adminSnapshot, error) {
	return d.snapshotWithDetail(ctx, selectedGroup, windowMinutes, false)
}

func (d *adminDashboard) snapshotWithDetail(ctx context.Context, selectedGroup int64, windowMinutes int, includeDetail bool) (adminSnapshot, error) {
	groups, err := d.loadGroups(ctx)
	if err != nil {
		return adminSnapshot{}, err
	}
	if selectedGroup == 0 && len(groups) > 0 {
		selectedGroup = groups[0].GroupID
	}
	stats, err := d.loadStats(ctx)
	if err != nil {
		return adminSnapshot{}, err
	}
	retrieval, err := d.loadRetrievalMetrics(ctx, selectedGroup, windowMinutes)
	if err != nil {
		return adminSnapshot{}, err
	}
	var persona adminPersona
	memories := []adminMemory{}
	relationships := []adminRelationship{}
	activity := []adminActivity{}
	if includeDetail {
		persona, err = d.loadPersona(ctx, selectedGroup)
		if err != nil {
			return adminSnapshot{}, err
		}
		memories, err = d.loadMemories(ctx, selectedGroup)
		if err != nil {
			return adminSnapshot{}, err
		}
		relationships, err = d.loadRelationships(ctx, selectedGroup)
		if err != nil {
			return adminSnapshot{}, err
		}
		activity, err = d.loadActivity(ctx, selectedGroup, windowMinutes)
		if err != nil {
			return adminSnapshot{}, err
		}
	}
	modelUsage, err := d.loadModelUsageMetrics(ctx, selectedGroup, windowMinutes)
	if err != nil {
		return adminSnapshot{}, err
	}
	windowMetrics, err := d.loadWindowMetrics(ctx, selectedGroup, windowMinutes)
	if err != nil {
		return adminSnapshot{}, err
	}
	lastErrorAt, err := d.loadLastTaskErrorAt(ctx)
	if err != nil {
		return adminSnapshot{}, err
	}
	mainModelStatus, vectorStatus, mainCheckedAt, vectorCheckedAt, err := d.loadCapabilityHealth(ctx)
	if err != nil {
		return adminSnapshot{}, err
	}
	return adminSnapshot{
		UpdatedAt: time.Now(), SelectedGroup: selectedGroup,
		Status: adminStatus{Mode: d.cfg.App.Mode, QQEnabled: d.cfg.QQ.Enabled, QQConnected: d.connected != nil && d.connected(), SelfID: d.cfg.QQ.SelfID, DatabaseOK: d.db.PingContext(ctx) == nil, QueueBacklog: stats.PendingTasks, LastErrorAt: lastErrorAt, MainModelStatus: mainModelStatus, VectorSearchStatus: vectorStatus, MainModelCheckedAt: mainCheckedAt, VectorCheckedAt: vectorCheckedAt},
		Stats:  stats, Persona: persona, Groups: groups, Memories: memories,
		Relationships: relationships, Activity: activity, Retrieval: retrieval, ModelUsage: modelUsage, WindowMinutes: windowMinutes, WindowMetrics: windowMetrics,
	}, nil
}

func (d *adminDashboard) loadCapabilityHealth(ctx context.Context) (string, string, *time.Time, *time.Time, error) {
	mainStatus := capabilityStatus(d.mainModelReady, "not_configured")
	vectorStatus := capabilityStatus(d.vectorSearchReady, "disabled")
	var mainCheckedAt, vectorCheckedAt sql.NullTime
	if d.health != nil {
		mainStatus, vectorStatus, mainChecked, vectorChecked := d.health.snapshot()
		if mainChecked != nil || vectorChecked != nil {
			return mainStatus, vectorStatus, mainChecked, vectorChecked, nil
		}
	}
	var mainError bool
	err := d.db.QueryRowContext(ctx, `
		SELECT created_at, (COALESCE(error, '') <> '')
		FROM model_usage_records ORDER BY created_at DESC LIMIT 1
	`).Scan(&mainCheckedAt, &mainError)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return "", "", nil, nil, fmt.Errorf("model health: %w", err)
	}
	if mainCheckedAt.Valid && d.mainModelReady {
		mainStatus = "ready"
		if mainError {
			mainStatus = "degraded"
		}
	}
	var vectorError bool
	err = d.db.QueryRowContext(ctx, `
		SELECT created_at, vector_error
		FROM retrieval_traces WHERE vector_enabled
		ORDER BY created_at DESC LIMIT 1
	`).Scan(&vectorCheckedAt, &vectorError)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return "", "", nil, nil, fmt.Errorf("vector health: %w", err)
	}
	if d.vectorSearchReady {
		vectorStatus = "idle"
		if vectorCheckedAt.Valid {
			vectorStatus = "ready"
			if vectorError {
				vectorStatus = "degraded"
			}
		}
	}
	var mainAt, vectorAt *time.Time
	if mainCheckedAt.Valid {
		mainAt = &mainCheckedAt.Time
	}
	if vectorCheckedAt.Valid {
		vectorAt = &vectorCheckedAt.Time
	}
	return mainStatus, vectorStatus, mainAt, vectorAt, nil
}

func capabilityStatus(ready bool, unavailable string) string {
	if ready {
		return "ready"
	}
	return unavailable
}

func (d *adminDashboard) loadLastTaskErrorAt(ctx context.Context) (*time.Time, error) {
	var last sql.NullTime
	if err := d.db.QueryRowContext(ctx, `
		SELECT NULLIF(GREATEST(
			COALESCE((SELECT MAX(updated_at) FROM async_outbox WHERE status = 'dead_letter' OR COALESCE(last_error, '') <> ''), TIMESTAMPTZ 'epoch'),
			COALESCE((SELECT MAX(created_at) FROM model_usage_records WHERE COALESCE(error, '') <> ''), TIMESTAMPTZ 'epoch')
		), TIMESTAMPTZ 'epoch')
	`).Scan(&last); err != nil {
		return nil, fmt.Errorf("task health: %w", err)
	}
	if !last.Valid {
		return nil, nil
	}
	return &last.Time, nil
}

func (d *adminDashboard) loadWindowMetrics(ctx context.Context, groupID int64, windowMinutes int) (adminWindowMetrics, error) {
	var metrics adminWindowMetrics
	err := d.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM thought_records WHERE created_at > NOW() - make_interval(mins => $2) AND ($1 = 0 OR group_id = $1)),
			(SELECT COUNT(*) FROM thought_records WHERE created_at > NOW() - make_interval(mins => $2) AND ($1 = 0 OR group_id = $1) AND chosen_action <> 'silent'),
			(SELECT COUNT(*) FROM thought_records WHERE created_at > NOW() - make_interval(mins => $2) AND ($1 = 0 OR group_id = $1) AND outcome = 'sent'),
			(SELECT COUNT(*) FROM async_outbox WHERE updated_at > NOW() - make_interval(mins => $2)),
			(SELECT COUNT(*) FROM async_outbox WHERE updated_at > NOW() - make_interval(mins => $2) AND (status = 'dead_letter' OR last_error <> ''))
	`, groupID, windowMinutes).Scan(&metrics.Decisions, &metrics.ActionDecisions, &metrics.Replies, &metrics.Tasks, &metrics.FailedTasks)
	if err != nil {
		return metrics, fmt.Errorf("window metrics: %w", err)
	}
	return metrics, nil
}

func loadAdminMetricSeries(ctx context.Context, db *sql.DB, groupID int64, windowMinutes int) (adminMetricSeries, error) {
	bucket, interval := "hour", "1 hour"
	if windowMinutes <= 60 {
		bucket, interval = "minute", "1 minute"
	}
	start := time.Now().Add(-time.Duration(windowMinutes) * time.Minute)
	rows, err := db.QueryContext(ctx, `
		WITH buckets AS (
			SELECT generate_series(date_trunc($3, $1::timestamptz), date_trunc($3, NOW()), $4::interval) AS at
		), retrieval AS (
			SELECT date_trunc($3, created_at) AS at, COUNT(*) AS queries,
			       COUNT(*) FILTER (WHERE jsonb_array_length(hit_memory_ids_json) > 0) AS hits,
			       COUNT(*) FILTER (WHERE jsonb_array_length(selected_memory_ids_json) > 0) AS selected
			FROM retrieval_traces WHERE created_at >= $1 AND ($2 = 0 OR group_id = $2)
			GROUP BY 1
		), decisions AS (
			SELECT date_trunc($3, created_at) AS at, COUNT(*) AS decisions,
			       COUNT(*) FILTER (WHERE outcome = 'sent') AS replies
			FROM thought_records WHERE created_at >= $1 AND ($2 = 0 OR group_id = $2)
			GROUP BY 1
		), model AS (
			SELECT date_trunc($3, created_at) AS at, COUNT(*) AS calls,
			       COUNT(*) FILTER (WHERE error <> '') AS errors,
			       AVG(duration_ms) AS avg_duration
			FROM model_usage_records WHERE created_at >= $1 AND ($2 = 0 OR group_id = $2)
			GROUP BY 1
		)
		SELECT b.at, COALESCE(r.queries, 0), COALESCE(r.hits, 0), COALESCE(r.selected, 0),
		       COALESCE(d.decisions, 0), COALESCE(d.replies, 0), COALESCE(m.calls, 0),
		       COALESCE(m.errors, 0), COALESCE(m.avg_duration, 0)
		FROM buckets b
		LEFT JOIN retrieval r USING (at)
		LEFT JOIN decisions d USING (at)
		LEFT JOIN model m USING (at)
		ORDER BY b.at ASC
	`, start, groupID, bucket, interval)
	if err != nil {
		return adminMetricSeries{}, fmt.Errorf("query metric series: %w", err)
	}
	defer rows.Close()
	points := make([]adminMetricPoint, 0, windowMinutes/5+2)
	for rows.Next() {
		var point adminMetricPoint
		if err := rows.Scan(&point.At, &point.Queries, &point.QueriesWithHits, &point.SelectedQueries,
			&point.Decisions, &point.Replies, &point.ModelCalls, &point.ModelErrors, &point.AvgDurationMS); err != nil {
			return adminMetricSeries{}, err
		}
		points = append(points, point)
	}
	if err := rows.Err(); err != nil {
		return adminMetricSeries{}, err
	}
	return adminMetricSeries{Points: points}, nil
}

func (d *adminDashboard) loadModelUsageMetrics(ctx context.Context, groupID int64, windowMinutes int) (adminModelUsageMetrics, error) {
	var metrics adminModelUsageMetrics
	var avg sql.NullFloat64
	err := d.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0), AVG(duration_ms),
		       COUNT(*) FILTER (WHERE error <> '')
		FROM model_usage_records
		WHERE created_at > NOW() - make_interval(mins => $2) AND ($1 = 0 OR group_id = $1)
	`, groupID, windowMinutes).Scan(&metrics.Calls, &metrics.InputTokens, &metrics.OutputTokens, &avg, &metrics.ErrorCalls)
	if err != nil {
		return metrics, fmt.Errorf("model usage metrics: %w", err)
	}
	if avg.Valid {
		metrics.AvgDurationMS = avg.Float64
	}
	return metrics, nil
}

func (d *adminDashboard) loadRetrievalMetrics(ctx context.Context, groupID int64, windowMinutes int) (adminRetrievalMetrics, error) {
	var metrics adminRetrievalMetrics
	var avg sql.NullFloat64
	err := d.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE jsonb_array_length(hit_memory_ids_json) > 0),
		       AVG(candidate_count),
		       COUNT(*) FILTER (WHERE outcome <> ''),
		       COUNT(*) FILTER (WHERE jsonb_array_length(selected_memory_ids_json) > 0)
		FROM retrieval_traces
		WHERE created_at > NOW() - make_interval(mins => $2) AND ($1 = 0 OR group_id = $1)
	`, groupID, windowMinutes).Scan(&metrics.Queries, &metrics.QueriesWithHits, &avg, &metrics.ResultRecordedQueries, &metrics.SelectedQueries)
	if err != nil {
		return metrics, fmt.Errorf("retrieval metrics: %w", err)
	}
	if avg.Valid {
		metrics.AvgCandidateCount = avg.Float64
	}
	if metrics.Queries > 0 {
		metrics.HitRate = float64(metrics.QueriesWithHits) / float64(metrics.Queries)
	}
	if metrics.QueriesWithHits > 0 {
		metrics.SelectionRate = float64(metrics.SelectedQueries) / float64(metrics.QueriesWithHits)
	}
	return metrics, nil
}

func (d *adminDashboard) loadGroups(ctx context.Context) ([]adminGroup, error) {
	rows, err := d.db.QueryContext(ctx, `
		WITH ids AS (
			SELECT group_id FROM messages WHERE group_id > 0 UNION SELECT group_id FROM member_profiles WHERE group_id > 0
			UNION SELECT group_id FROM relationships WHERE group_id > 0 UNION SELECT group_id FROM group_working_memory WHERE group_id > 0
			UNION SELECT group_id FROM thought_records WHERE group_id > 0 UNION SELECT group_id FROM retrieval_traces WHERE group_id > 0
		), message_stats AS (
			SELECT group_id, COUNT(*) AS messages, MAX(occurred_at) AS last_activity FROM messages WHERE group_id > 0 GROUP BY group_id
		), member_stats AS (
			SELECT group_id, COUNT(*) AS members FROM member_profiles WHERE group_id > 0 GROUP BY group_id
		)
		SELECT ids.group_id, COALESCE(ms.messages, 0), COALESCE(ps.members, 0),
		       COALESCE(gwm.state_json->>'active_topic', ''), ms.last_activity
		FROM ids
		LEFT JOIN message_stats ms USING (group_id)
		LEFT JOIN member_stats ps USING (group_id)
		LEFT JOIN group_working_memory gwm USING (group_id)
	`)
	if err != nil {
		return nil, fmt.Errorf("groups: %w", err)
	}
	defer rows.Close()
	byID := make(map[int64]adminGroup)
	for rows.Next() {
		var group adminGroup
		var last sql.NullTime
		if err := rows.Scan(&group.GroupID, &group.Messages, &group.Members, &group.ActiveTopic, &last); err != nil {
			return nil, err
		}
		if last.Valid {
			group.LastActivity = last.Time
		}
		byID[group.GroupID] = group
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, groupID := range d.cfg.QQ.GroupWhitelist {
		if groupID <= 0 {
			continue
		}
		if _, ok := byID[groupID]; !ok {
			byID[groupID] = adminGroup{GroupID: groupID}
		}
	}
	groupNames := d.loadGroupNames(ctx)
	groups := make([]adminGroup, 0, len(byID))
	for _, group := range byID {
		group.GroupName = groupNames[group.GroupID]
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].LastActivity.Equal(groups[j].LastActivity) {
			return groups[i].GroupID < groups[j].GroupID
		}
		return groups[i].LastActivity.After(groups[j].LastActivity)
	})
	return groups, nil
}

func (d *adminDashboard) loadGroupNames(ctx context.Context) map[int64]string {
	if d.groupClient == nil {
		return nil
	}

	d.groupNamesMu.Lock()
	if time.Since(d.groupNamesAt) < 5*time.Minute {
		names := cloneGroupNames(d.groupNames)
		d.groupNamesMu.Unlock()
		return names
	}
	d.groupNamesMu.Unlock()

	groups, err := d.groupClient.API().GetGroupList(ctx, api.GetGroupListRequest{})
	now := time.Now()
	if err != nil {
		slog.Warn("admin: load group names failed", "error", err)
		d.groupNamesMu.Lock()
		d.groupNamesAt = now
		names := cloneGroupNames(d.groupNames)
		d.groupNamesMu.Unlock()
		return names
	}

	names := make(map[int64]string, len(*groups))
	for _, group := range *groups {
		name := strings.TrimSpace(group.GroupName)
		if name != "" {
			names[int64(group.GroupID)] = name
		}
	}
	d.groupNamesMu.Lock()
	d.groupNames = names
	d.groupNamesAt = now
	d.groupNamesMu.Unlock()
	return cloneGroupNames(names)
}

func cloneGroupNames(names map[int64]string) map[int64]string {
	if len(names) == 0 {
		return nil
	}
	clone := make(map[int64]string, len(names))
	for id, name := range names {
		clone[id] = name
	}
	return clone
}

func (d *adminDashboard) loadStats(ctx context.Context) (adminStats, error) {
	var stats adminStats
	err := d.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(DISTINCT group_id) FROM messages WHERE group_id > 0),
			(SELECT COUNT(*) FROM member_profiles),
			(SELECT COUNT(*) FROM memories WHERE expires_at IS NULL OR expires_at > NOW()),
			(SELECT COUNT(*) FROM async_outbox WHERE status IN ('pending', 'running', 'retry'))
	`).Scan(&stats.Groups, &stats.Members, &stats.Memories, &stats.PendingTasks)
	return stats, err
}

func (d *adminDashboard) loadPersona(ctx context.Context, groupID int64) (adminPersona, error) {
	runtimeFacts, err := d.facts.CurrentPersonaFacts(ctx, d.definition.Config.ID, time.Now())
	if err != nil {
		return adminPersona{}, fmt.Errorf("persona facts: %w", err)
	}
	view := personadomain.ResolveView(d.definition, runtimeFacts, time.Now())
	persona := adminPersona{
		ID: d.definition.Config.ID, Name: d.definition.Config.Name,
		Description: d.definition.Config.Description, Facts: view.Facts,
		Interests: append([]string{}, d.definition.Config.Interests...),
		Mood:      "steady", Energy: "normal",
	}
	if persona.Facts == nil {
		persona.Facts = []personadomain.PersonaFact{}
	}
	if groupID == 0 {
		return persona, nil
	}
	state, err := d.state.GetPersonaState(ctx, persona.ID, 0)
	if err != nil {
		return adminPersona{}, fmt.Errorf("persona state: %w", err)
	}
	runtime, err := d.state.GetRuntimeState(ctx, groupID)
	if err != nil {
		return adminPersona{}, fmt.Errorf("runtime state: %w", err)
	}
	persona.Mood, persona.Energy, persona.TalkBias, persona.Runtime = state.Mood, state.Energy, state.TalkBias, runtime
	return persona, nil
}

func (d *adminDashboard) loadMemories(ctx context.Context, groupID int64) ([]adminMemory, error) {
	return loadAdminMemories(ctx, d.db, groupID, "active", "")
}

func loadAdminMemories(ctx context.Context, db *sql.DB, groupID int64, status, query string) ([]adminMemory, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT memory_id, scope, type, subject, content, confidence, importance, created_at, expires_at, source_event_id, updated_at
		FROM memories
		WHERE ($2 = 'all' OR ($2 = 'active' AND (expires_at IS NULL OR expires_at > NOW())) OR ($2 = 'expired' AND expires_at IS NOT NULL AND expires_at <= NOW()))
		  AND ($1 = 0 OR scope = 'global' OR scope = 'group:' || $1::text OR scope LIKE 'group:' || $1::text || ':user:%')
		  AND ($3 = '' OR LOWER(subject || ' ' || content || ' ' || scope) LIKE '%' || LOWER($3) || '%')
		ORDER BY created_at DESC LIMIT 200
	`, groupID, status, strings.TrimSpace(query))
	if err != nil {
		return nil, fmt.Errorf("memories: %w", err)
	}
	defer rows.Close()
	memories := []adminMemory{}
	for rows.Next() {
		var memory adminMemory
		var expires sql.NullTime
		if err := rows.Scan(&memory.ID, &memory.Scope, &memory.Type, &memory.Subject, &memory.Content,
			&memory.Confidence, &memory.Importance, &memory.CreatedAt, &expires, &memory.SourceEventID, &memory.UpdatedAt); err != nil {
			return nil, err
		}
		if expires.Valid {
			memory.ExpiresAt = &expires.Time
		}
		memories = append(memories, memory)
	}
	return memories, rows.Err()
}

func loadAdminMemoryPage(ctx context.Context, db *sql.DB, groupID int64, status, memoryType, query string, page, pageSize int) (adminMemoryPage, error) {
	where := ` FROM memories
		WHERE ($2 = 'all' OR ($2 = 'active' AND (expires_at IS NULL OR expires_at > NOW())) OR ($2 = 'expired' AND expires_at IS NOT NULL AND expires_at <= NOW()))
		  AND ($1 = 0 OR scope = 'global' OR scope = 'group:' || $1::text OR scope LIKE 'group:' || $1::text || ':user:%')
		  AND ($3 = '' OR type = $3)
		  AND ($4 = '' OR LOWER(subject || ' ' || content || ' ' || scope) LIKE '%' || LOWER($4) || '%')`
	args := []any{groupID, status, memoryType, strings.TrimSpace(query)}
	var total int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*)"+where, args...).Scan(&total); err != nil {
		return adminMemoryPage{}, fmt.Errorf("count memories: %w", err)
	}
	offset := (page - 1) * pageSize
	querySQL := `SELECT memory_id, scope, type, subject, content, confidence, importance, created_at, expires_at, source_event_id, updated_at` + where + fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	args = append(args, pageSize, offset)
	rows, err := db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return adminMemoryPage{}, fmt.Errorf("query memories: %w", err)
	}
	defer rows.Close()
	items := make([]adminMemory, 0, pageSize)
	for rows.Next() {
		var memory adminMemory
		var expires sql.NullTime
		if err := rows.Scan(&memory.ID, &memory.Scope, &memory.Type, &memory.Subject, &memory.Content,
			&memory.Confidence, &memory.Importance, &memory.CreatedAt, &expires, &memory.SourceEventID, &memory.UpdatedAt); err != nil {
			return adminMemoryPage{}, err
		}
		if expires.Valid {
			memory.ExpiresAt = &expires.Time
		}
		items = append(items, memory)
	}
	if err := rows.Err(); err != nil {
		return adminMemoryPage{}, err
	}
	return adminMemoryPage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func loadAdminMemes(ctx context.Context, db *sql.DB, groupID int64, query string) ([]adminMeme, error) {
	page, err := loadAdminMemePage(ctx, db, groupID, query, 1, 200, "")
	return page.Items, err
}

func loadAdminMemePage(ctx context.Context, db *sql.DB, groupID int64, query string, page, pageSize int, storagePath string) (adminMemePage, error) {
	query = strings.TrimSpace(query)
	where := ` FROM meme_assets a
		JOIN meme_descriptors d ON d.meme_id = a.meme_id
		WHERE ($1 = 0 OR a.group_id = 0)
		  AND ($2 = '' OR d.title ILIKE '%' || $2 || '%' OR d.summary ILIKE '%' || $2 || '%'
		       OR d.keywords_json::text ILIKE '%' || $2 || '%'
		       OR d.emotion_tags_json::text ILIKE '%' || $2 || '%'
		       OR d.scene_tags_json::text ILIKE '%' || $2 || '%')`
	args := []any{groupID, query}
	var total int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*)"+where, args...).Scan(&total); err != nil {
		return adminMemePage{}, fmt.Errorf("count memes: %w", err)
	}
	selectSQL := `SELECT a.meme_id, a.group_id, a.source_event_id, a.object_key, a.file_ext,
		       a.width, a.height, a.animated, a.status, a.send_count, a.dud_count,
		       a.created_at, a.last_sent_at, d.title, d.summary, d.keywords_json,
		       d.emotion_tags_json, d.scene_tags_json, d.confidence, d.reviewed` + where + fmt.Sprintf(" ORDER BY a.created_at DESC LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	offset := (page - 1) * pageSize
	args = append(args, pageSize, offset)
	rows, err := db.QueryContext(ctx, selectSQL, args...)
	if err != nil {
		return adminMemePage{}, fmt.Errorf("query memes: %w", err)
	}
	defer rows.Close()
	memes := make([]adminMeme, 0, pageSize)
	for rows.Next() {
		var meme adminMeme
		var keywords, emotions, scenes []byte
		if err := rows.Scan(&meme.MemeID, &meme.GroupID, &meme.SourceEventID, &meme.ObjectKey, &meme.FileExt,
			&meme.Width, &meme.Height, &meme.Animated, &meme.Status, &meme.SendCount, &meme.DudCount,
			&meme.CreatedAt, &meme.LastSentAt, &meme.Title, &meme.Summary, &keywords, &emotions, &scenes,
			&meme.Confidence, &meme.Reviewed); err != nil {
			return adminMemePage{}, err
		}
		meme.PreviewURL = memePreviewURL(storagePath, meme.ObjectKey)
		_ = json.Unmarshal(keywords, &meme.Keywords)
		_ = json.Unmarshal(emotions, &meme.EmotionTags)
		_ = json.Unmarshal(scenes, &meme.SceneTags)
		memes = append(memes, meme)
	}
	if err := rows.Err(); err != nil {
		return adminMemePage{}, err
	}
	return adminMemePage{Items: memes, Total: total, Page: page, PageSize: pageSize}, nil
}

func memePreviewURL(storagePath, objectKey string) string {
	if strings.TrimSpace(storagePath) == "" || objectKey == "" || objectKey != filepath.Base(objectKey) {
		return ""
	}
	if _, err := os.Stat(filepath.Join(storagePath, objectKey)); err != nil {
		return ""
	}
	return "/admin/api/memes/files/" + url.PathEscape(objectKey)
}

func (d *adminDashboard) loadRelationships(ctx context.Context, groupID int64) ([]adminRelationship, error) {
	page, err := loadAdminRelationshipPage(ctx, d.db, d.definition.Config.ID, groupID, "", 1, 100)
	return page.Items, err
}

func loadAdminRelationshipPage(ctx context.Context, db *sql.DB, personaID string, groupID int64, query string, page, pageSize int) (adminRelationshipPage, error) {
	where := ` FROM relationships r
		LEFT JOIN member_profiles p ON p.group_id = r.group_id AND p.user_id = r.user_id
		WHERE r.persona_id = $1 AND ($2 = 0 OR r.group_id = $2)
		  AND ($3 = '' OR LOWER(COALESCE(NULLIF(p.group_card, ''), NULLIF(p.nickname, ''), NULLIF(p.qq_nickname, ''), r.user_id::text) || ' ' || r.user_id::text) LIKE '%' || LOWER($3) || '%')`
	args := []any{personaID, groupID, strings.TrimSpace(query)}
	var total int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*)"+where, args...).Scan(&total); err != nil {
		return adminRelationshipPage{}, fmt.Errorf("count relationships: %w", err)
	}
	offset := (page - 1) * pageSize
	querySQL := `SELECT r.group_id, r.user_id,
		       COALESCE(NULLIF(p.group_card, ''), NULLIF(p.nickname, ''), NULLIF(p.qq_nickname, ''), r.user_id::text),
		       r.affinity, r.familiarity, r.tease_tolerance, r.grudge_score,
		       COALESCE(p.message_count, 0), r.last_interact_at` + where + fmt.Sprintf(" ORDER BY r.affinity DESC, r.last_interact_at DESC LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	args = append(args, pageSize, offset)
	rows, err := db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return adminRelationshipPage{}, fmt.Errorf("query relationships: %w", err)
	}
	defer rows.Close()
	relationships := make([]adminRelationship, 0, pageSize)
	for rows.Next() {
		var relationship adminRelationship
		if err := rows.Scan(&relationship.GroupID, &relationship.UserID, &relationship.Name,
			&relationship.Affinity, &relationship.Familiarity, &relationship.TeaseTolerance,
			&relationship.GrudgeScore, &relationship.MessageCount, &relationship.LastInteractAt); err != nil {
			return adminRelationshipPage{}, err
		}
		relationships = append(relationships, relationship)
	}
	if err := rows.Err(); err != nil {
		return adminRelationshipPage{}, err
	}
	return adminRelationshipPage{Items: relationships, Total: total, Page: page, PageSize: pageSize}, nil
}

func (d *adminDashboard) loadActivity(ctx context.Context, groupID int64, windowMinutes int) ([]adminActivity, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT event_id, at, group_id, type, label, subject, detail FROM (
			SELECT event_id, occurred_at AS at, group_id, 'message' AS type, kind AS label,
			       COALESCE(NULLIF(sender_group_card, ''), NULLIF(sender_qq_nickname, ''), user_id::text) AS subject,
			       LEFT(text_content, 300) AS detail
			FROM messages WHERE group_id > 0 AND occurred_at > NOW() - make_interval(mins => $2) AND ($1 = 0 OR group_id = $1)
			UNION ALL
			SELECT event_id, created_at, group_id, 'decision', chosen_action, outcome, LEFT(interpretation, 300)
			FROM thought_records WHERE group_id > 0 AND created_at > NOW() - make_interval(mins => $2) AND ($1 = 0 OR group_id = $1)
		) activity
		ORDER BY at DESC LIMIT 80
	`, groupID, windowMinutes)
	if err != nil {
		return nil, fmt.Errorf("activity: %w", err)
	}
	defer rows.Close()
	activity := []adminActivity{}
	for rows.Next() {
		var item adminActivity
		if err := rows.Scan(&item.EventID, &item.At, &item.GroupID, &item.Type, &item.Label, &item.Subject, &item.Detail); err != nil {
			return nil, err
		}
		activity = append(activity, item)
	}
	return activity, rows.Err()
}

func loadAdminActivityPage(ctx context.Context, db *sql.DB, groupID int64, windowMinutes int, activityType string, page, pageSize int) (adminActivityPage, error) {
	const source = `
		SELECT event_id, at, group_id, type, label, subject, detail FROM (
			SELECT event_id, occurred_at AS at, group_id, 'message' AS type, kind AS label,
			       COALESCE(NULLIF(sender_group_card, ''), NULLIF(sender_qq_nickname, ''), user_id::text) AS subject,
			       LEFT(text_content, 300) AS detail
			FROM messages WHERE group_id > 0 AND occurred_at > NOW() - make_interval(mins => $1) AND ($2 = 0 OR group_id = $2)
			UNION ALL
			SELECT event_id, created_at, group_id, 'decision', chosen_action, outcome, LEFT(interpretation, 300)
			FROM thought_records WHERE group_id > 0 AND created_at > NOW() - make_interval(mins => $1) AND ($2 = 0 OR group_id = $2)
		) activity`
	args := []any{windowMinutes, groupID, activityType}
	where := " WHERE ($3 = '' OR type = $3)"
	var total int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM ("+source+") activity"+where, args...).Scan(&total); err != nil {
		return adminActivityPage{}, fmt.Errorf("count activity: %w", err)
	}
	var messageCount, decisionCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FILTER (WHERE type = 'message'), COUNT(*) FILTER (WHERE type = 'decision') FROM ("+source+") activity", windowMinutes, groupID).Scan(&messageCount, &decisionCount); err != nil {
		return adminActivityPage{}, fmt.Errorf("count activity types: %w", err)
	}
	offset := (page - 1) * pageSize
	query := "SELECT event_id, at, group_id, type, label, subject, detail FROM (" + source + ") activity" + where + fmt.Sprintf(" ORDER BY at DESC LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	args = append(args, pageSize, offset)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return adminActivityPage{}, fmt.Errorf("query activity: %w", err)
	}
	defer rows.Close()
	items := make([]adminActivity, 0, pageSize)
	for rows.Next() {
		var item adminActivity
		if err := rows.Scan(&item.EventID, &item.At, &item.GroupID, &item.Type, &item.Label, &item.Subject, &item.Detail); err != nil {
			return adminActivityPage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return adminActivityPage{}, err
	}
	return adminActivityPage{Items: items, Total: total, MessageCount: messageCount, DecisionCount: decisionCount, Page: page, PageSize: pageSize}, nil
}

func loadAdminEventDetail(ctx context.Context, db *sql.DB, eventID string) (adminEventDetail, error) {
	var detail adminEventDetail
	var senderCard, nickname sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT event_id, message_id, group_id, user_id, kind, text_content,
		       sender_group_card, sender_qq_nickname, occurred_at
		FROM messages WHERE event_id = $1
	`, eventID).Scan(&detail.EventID, &detail.MessageID, &detail.GroupID, &detail.UserID, &detail.Kind, &detail.Text, &senderCard, &nickname, &detail.OccurredAt)
	if err != nil {
		return detail, err
	}
	detail.Sender = senderCard.String
	if detail.Sender == "" {
		detail.Sender = nickname.String
	}
	rows, err := db.QueryContext(ctx, `
		SELECT trace_id, query, candidate_count, hit_memory_ids_json, selected_memory_ids_json, outcome, created_at
		FROM retrieval_traces WHERE event_id = $1 ORDER BY created_at ASC
	`, eventID)
	if err != nil {
		return detail, err
	}
	defer rows.Close()
	detail.Retrievals = []adminRetrievalDetail{}
	detail.ModelUsages = []adminModelUsageDetail{}
	for rows.Next() {
		var item adminRetrievalDetail
		var hits, selected []byte
		if err := rows.Scan(&item.TraceID, &item.Query, &item.CandidateCount, &hits, &selected, &item.Outcome, &item.CreatedAt); err != nil {
			return detail, err
		}
		_ = json.Unmarshal(hits, &item.HitMemoryIDs)
		_ = json.Unmarshal(selected, &item.SelectedIDs)
		detail.Retrievals = append(detail.Retrievals, item)
	}
	if err := rows.Err(); err != nil {
		return detail, err
	}
	modelRows, err := db.QueryContext(ctx, `
		SELECT trace_id, iteration, input_tokens, output_tokens, duration_ms, tools_json, tool_calls_json, usage_available, error, sent, final_action, drop_reason, created_at
		FROM model_usage_records WHERE event_id = $1
		ORDER BY created_at ASC
	`, eventID)
	if err == nil {
		defer modelRows.Close()
		for modelRows.Next() {
			var item adminModelUsageDetail
			var tools, toolCalls []byte
			if err := modelRows.Scan(&item.TraceID, &item.Iteration, &item.InputTokens, &item.OutputTokens, &item.DurationMS, &tools, &toolCalls, &item.UsageAvailable, &item.Error, &item.Sent, &item.FinalAction, &item.DropReason, &item.CreatedAt); err != nil {
				return detail, err
			}
			_ = json.Unmarshal(tools, &item.Tools)
			_ = json.Unmarshal(toolCalls, &item.ToolCalls)
			detail.ModelUsages = append(detail.ModelUsages, item)
		}
		if err := modelRows.Err(); err != nil {
			return detail, err
		}
	}
	var decision adminDecisionDetail
	var evidence []byte
	err = db.QueryRowContext(ctx, `
		SELECT thought_id, chosen_action, outcome, interpretation, evidence_json, uncertainty, created_at
		FROM thought_records WHERE event_id = $1 ORDER BY created_at DESC LIMIT 1
	`, eventID).Scan(&decision.ThoughtID, &decision.Action, &decision.Outcome, &decision.Interpretation, &evidence, &decision.Uncertainty, &decision.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		setAdminEventDuration(&detail)
		return detail, nil
	}
	if err != nil {
		return detail, err
	}
	_ = json.Unmarshal(evidence, &decision.Evidence)
	detail.Decision = &decision
	setAdminEventDuration(&detail)
	return detail, nil
}

func setAdminEventDuration(detail *adminEventDetail) {
	latest := detail.OccurredAt
	if detail.Decision != nil && detail.Decision.CreatedAt.After(latest) {
		latest = detail.Decision.CreatedAt
	}
	for _, item := range detail.Retrievals {
		if item.CreatedAt.After(latest) {
			latest = item.CreatedAt
		}
	}
	for _, item := range detail.ModelUsages {
		if item.CreatedAt.After(latest) {
			latest = item.CreatedAt
		}
	}
	if latest.After(detail.OccurredAt) {
		detail.DurationMS = latest.Sub(detail.OccurredAt).Milliseconds()
	}
}
