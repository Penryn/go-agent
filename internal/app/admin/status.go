package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// SystemStatus 系统状态（轻量级）
type SystemStatus struct {
	ActiveGroups  int       `json:"active_groups"`
	TotalPersonas int       `json:"total_personas"`
	TotalMemories int       `json:"total_memories"`
	LastActivity  time.Time `json:"last_activity"`
	Timestamp     int64     `json:"timestamp"`
}

// handleStatus 处理状态查询（轻量级，高频刷新）
// GET /api/admin/status
func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 使用现有的 load 函数获取快照
	snapshot, err := h.load(ctx, 0) // 0 表示获取所有群组
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	status := h.buildSystemStatus(snapshot)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// buildSystemStatus 从快照构建系统状态
func (h *Handler) buildSystemStatus(snapshot Snapshot) *SystemStatus {
	now := time.Now()

	// 统计活跃群组（24小时内有消息）
	activeGroups := 0
	var lastActivity time.Time

	for _, group := range snapshot.Groups {
		if group.LastActivity.IsZero() {
			continue
		}
		if now.Sub(group.LastActivity) < 24*time.Hour {
			activeGroups++
		}
		if group.LastActivity.After(lastActivity) {
			lastActivity = group.LastActivity
		}
	}

	return &SystemStatus{
		ActiveGroups:  activeGroups,
		TotalPersonas: 1, // 当前只有一个人格
		TotalMemories: len(snapshot.Memories),
		LastActivity:  lastActivity,
		Timestamp:     now.Unix(),
	}
}

// IncrementalUpdate 增量更新结构
type IncrementalUpdate struct {
	Since     int64                  `json:"since"`
	Updates   map[string]interface{} `json:"updates"`
	Deletes   map[string][]int64     `json:"deletes"`
	Timestamp int64                  `json:"timestamp"`
}

// handleUpdates 处理增量更新查询
// GET /api/admin/updates?since=1234567890
func (h *Handler) handleUpdates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 解析 since 参数
	sinceStr := r.URL.Query().Get("since")
	var since int64
	if sinceStr != "" {
		_, _ = fmt.Sscanf(sinceStr, "%d", &since)
	}

	// 使用现有的 load 函数
	snapshot, err := h.load(ctx, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	update := h.buildIncrementalUpdate(snapshot, since)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(update)
}

// buildIncrementalUpdate 构建增量更新
func (h *Handler) buildIncrementalUpdate(snapshot Snapshot, since int64) *IncrementalUpdate {
	now := time.Now()
	sinceTime := time.Unix(since, 0)

	update := &IncrementalUpdate{
		Since:     since,
		Updates:   make(map[string]interface{}),
		Deletes:   make(map[string][]int64),
		Timestamp: now.Unix(),
	}

	// 过滤更新的群组
	var updatedGroups []GroupSummary
	for _, group := range snapshot.Groups {
		if group.LastActivity.After(sinceTime) {
			updatedGroups = append(updatedGroups, GroupSummary{
				ID:            group.GroupID,
				Name:          group.GroupName,
				MemberCount:   group.Members,
				LastMessage:   truncate(group.ActiveTopic, 50),
				LastMessageAt: group.LastActivity,
			})
		}
	}

	if len(updatedGroups) > 0 {
		update.Updates["groups"] = updatedGroups
	}

	return update
}

// GroupSummary 群组摘要（轻量级）
type GroupSummary struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	MemberCount   int       `json:"member_count"`
	LastMessage   string    `json:"last_message"`
	LastMessageAt time.Time `json:"last_message_at"`
}

// truncate 截断字符串
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
