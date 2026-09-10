package admin

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
	"time"

	napcatsdk "github.com/zjutjh/napcat-sdk"

	toolsvc "github.com/phlin/go-agent/internal/application/tools"
	"github.com/phlin/go-agent/internal/application/ports"
	"github.com/phlin/go-agent/internal/config"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
)

type Handler struct {
	token           string
	personaID       string
	load            func(context.Context, int64) (Snapshot, error)
	loadWindow      func(context.Context, int64, int) (Snapshot, error)
	loadCore        func(context.Context, int64, int) (Snapshot, error)
	assets          http.Handler
	db              *sql.DB
	mcp             *toolsvc.MCPManager
	memeStoragePath string
}

func NewHandler(
	db *sql.DB,
	state ports.RuntimeStateStore,
	facts ports.PersonaFactStore,
	definition personadomain.PersonaDefinition,
	cfg config.Config,
	connected func() bool,
	mcp *toolsvc.MCPManager,
	mainModelReady, vectorSearchReady bool,
	health *CapabilityHealth,
	assets embed.FS,
) http.Handler {
	dashboard := &Dashboard{
		db:                db,
		state:             state,
		facts:             facts,
		definition:        definition,
		cfg:               cfg,
		connected:         connected,
		mainModelReady:    mainModelReady,
		vectorSearchReady: vectorSearchReady,
		health:            health,
	}
	if strings.TrimSpace(cfg.QQ.OutboundURL) != "" {
		dashboard.groupClient = napcatsdk.NewHTTPClient(
			cfg.QQ.OutboundURL,
			napcatsdk.WithToken(cfg.QQ.OutboundToken),
			napcatsdk.WithHTTPTimeout(2*time.Second),
		)
	}
	assetsFS, _ := fs.Sub(assets, "adminui/dist")
	return &Handler{
		token:           strings.TrimSpace(cfg.Server.AdminToken),
		personaID:       definition.Config.ID,
		load:            dashboard.snapshot,
		loadWindow:      dashboard.snapshotWindow,
		loadCore:        dashboard.snapshotCore,
		mcp:             mcp,
		db:              db,
		memeStoragePath: cfg.Meme.StoragePath,
		assets:          http.StripPrefix("/admin/", http.FileServer(http.FS(assetsFS))),
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	if strings.HasPrefix(r.URL.Path, "/admin/api/relationships/") {
		h.handleRelationshipDetail(w, r)
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
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; img-src 'self' data: https: http:")
		h.assets.ServeHTTP(w, r)
	}
}

func (h *Handler) handleMemeFile(w http.ResponseWriter, r *http.Request) {
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
	if err != nil || name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) {
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

func (h *Handler) authorizedQueryToken(r *http.Request) bool {
	if h.token == "" {
		return false
	}
	provided := strings.TrimSpace(r.URL.Query().Get("token"))
	return len(provided) == len(h.token) && subtle.ConstantTimeCompare([]byte(provided), []byte(h.token)) == 1
}

func (h *Handler) handleActivity(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) handleMetrics(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) handleMemories(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) handleMeme(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) memeFilePath(name string) (string, error) {
	base, err := filepath.Abs(strings.TrimSpace(h.memeStoragePath))
	if err != nil || strings.TrimSpace(h.memeStoragePath) == "" {
		return "", fmt.Errorf("meme storage path is not configured")
	}
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\\`) {
		return "", fmt.Errorf("invalid meme file")
	}
	return filepath.Join(base, name), nil
}

func (h *Handler) handleMemes(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) handleRelationships(w http.ResponseWriter, r *http.Request) {
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

// handleRelationshipDetail 处理单个关系的详细信息请求
// 路径格式: /admin/api/relationships/{group_id}/{user_id}/events
//          /admin/api/relationships/{group_id}/{user_id}/projection-history
func (h *Handler) handleRelationshipDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r) {
		http.Error(w, "admin token required", http.StatusUnauthorized)
		return
	}

	// 解析路径: /admin/api/relationships/{group_id}/{user_id}/{action}
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/admin/api/relationships/"), "/")
	if len(pathParts) < 3 {
		http.Error(w, "invalid path format", http.StatusBadRequest)
		return
	}

	groupID, err := strconv.ParseInt(pathParts[0], 10, 64)
	if err != nil || groupID <= 0 {
		http.Error(w, "invalid group_id", http.StatusBadRequest)
		return
	}

	userID, err := strconv.ParseInt(pathParts[1], 10, 64)
	if err != nil || userID <= 0 {
		http.Error(w, "invalid user_id", http.StatusBadRequest)
		return
	}

	action := pathParts[2]
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()

	switch action {
	case "events":
		h.handleRelationshipEvents(w, ctx, groupID, userID)
	case "projection-history":
		h.handleRelationshipProjectionHistory(w, ctx, groupID, userID)
	default:
		http.Error(w, "unknown action: "+action, http.StatusBadRequest)
	}
}

func (h *Handler) handleRelationshipEvents(w http.ResponseWriter, ctx context.Context, groupID, userID int64) {
	events, err := loadRelationshipEvents(ctx, h.db, h.personaID, groupID, userID)
	if err != nil {
		http.Error(w, "load relationship events: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, events)
}

func (h *Handler) handleRelationshipProjectionHistory(w http.ResponseWriter, ctx context.Context, groupID, userID int64) {
	history, err := loadRelationshipProjectionHistory(ctx, h.db, h.personaID, groupID, userID)
	if err != nil {
		http.Error(w, "load projection history: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, history)
}

func (h *Handler) handleTasks(w http.ResponseWriter, r *http.Request) {
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
	tasks := []Task{}
	for rows.Next() {
		var task Task
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
	writeJSON(w, TaskPage{Items: tasks, Total: total, Page: page, PageSize: pageSize})
}

func summarizeTaskPayload(payload []byte) TaskContext {
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil {
		return TaskContext{PayloadKeys: []string{}}
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	context := TaskContext{PayloadKeys: keys}
	if raw, ok := fields["group_id"]; ok {
		var groupID int64
		if json.Unmarshal(raw, &groupID) == nil && groupID > 0 {
			context.GroupID = groupID
		}
	}
	return context
}

func (h *Handler) handleTask(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) handleEventDetail(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) handleMCP(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) handleSnapshot(w http.ResponseWriter, r *http.Request) {
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
	var snapshot Snapshot
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

func normalizeAdminSnapshot(snapshot *Snapshot) {
	if snapshot.Groups == nil {
		snapshot.Groups = []Group{}
	}
	if snapshot.Memories == nil {
		snapshot.Memories = []Memory{}
	}
	if snapshot.Relationships == nil {
		snapshot.Relationships = []Relationship{}
	}
	if snapshot.Activity == nil {
		snapshot.Activity = []Activity{}
	}
	if snapshot.Persona.Facts == nil {
		snapshot.Persona.Facts = []personadomain.PersonaFact{}
	}
	if snapshot.Persona.Interests == nil {
		snapshot.Persona.Interests = []string{}
	}
}

func (h *Handler) authorized(r *http.Request) bool {
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

