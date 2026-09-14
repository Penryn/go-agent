package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	napcatsdk "github.com/zjutjh/napcat-sdk"
	"github.com/zjutjh/napcat-sdk/api"

	"github.com/phlin/go-agent/internal/application/ports"
	"github.com/phlin/go-agent/internal/config"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
)

type Dashboard struct {
	db                *sql.DB
	state             ports.RuntimeStateStore
	facts             ports.PersonaFactStore
	definition        personadomain.PersonaDefinition
	cfg               config.Config
	connected         func() bool
	mainModelReady    bool
	vectorSearchReady bool
	health            *CapabilityHealth
	groupClient       *napcatsdk.Client
	groupNamesMu      sync.Mutex
	groupNames        map[int64]string
	groupNamesAt      time.Time
}

func (d *Dashboard) snapshot(ctx context.Context, selectedGroup int64) (Snapshot, error) {
	return d.snapshotWindow(ctx, selectedGroup, 1440)
}

func (d *Dashboard) snapshotWindow(ctx context.Context, selectedGroup int64, windowMinutes int) (Snapshot, error) {
	return d.snapshotWithDetail(ctx, selectedGroup, windowMinutes, true)
}

func (d *Dashboard) snapshotCore(ctx context.Context, selectedGroup int64, windowMinutes int) (Snapshot, error) {
	return d.snapshotWithDetail(ctx, selectedGroup, windowMinutes, false)
}

func (d *Dashboard) snapshotWithDetail(ctx context.Context, selectedGroup int64, windowMinutes int, includeDetail bool) (Snapshot, error) {
	groups, err := d.loadGroups(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if selectedGroup == 0 && len(groups) > 0 {
		selectedGroup = groups[0].GroupID
	}
	stats, err := d.loadStats(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	retrieval, err := d.loadRetrievalMetrics(ctx, selectedGroup, windowMinutes)
	if err != nil {
		return Snapshot{}, err
	}
	var persona Persona
	memories := []Memory{}
	relationships := []Relationship{}
	activity := []Activity{}
	if includeDetail {
		persona, err = d.loadPersona(ctx, selectedGroup)
		if err != nil {
			return Snapshot{}, err
		}
		memories, err = d.loadMemories(ctx, selectedGroup)
		if err != nil {
			return Snapshot{}, err
		}
		relationships, err = d.loadRelationships(ctx, selectedGroup)
		if err != nil {
			return Snapshot{}, err
		}
		activity, err = d.loadActivity(ctx, selectedGroup, windowMinutes)
		if err != nil {
			return Snapshot{}, err
		}
	}
	modelUsage, err := d.loadModelUsageMetrics(ctx, selectedGroup, windowMinutes)
	if err != nil {
		return Snapshot{}, err
	}
	windowMetrics, err := d.loadWindowMetrics(ctx, selectedGroup, windowMinutes)
	if err != nil {
		return Snapshot{}, err
	}
	lastErrorAt, err := d.loadLastTaskErrorAt(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	mainModelStatus, vectorStatus, mainCheckedAt, vectorCheckedAt, err := d.loadCapabilityHealth(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		UpdatedAt: time.Now(), SelectedGroup: selectedGroup,
		Status: Status{Mode: d.cfg.App.Mode, QQEnabled: d.cfg.QQ.Enabled, QQConnected: d.connected != nil && d.connected(), SelfID: d.cfg.QQ.SelfID, DatabaseOK: d.db.PingContext(ctx) == nil, QueueBacklog: stats.PendingTasks, LastErrorAt: lastErrorAt, MainModelStatus: mainModelStatus, VectorSearchStatus: vectorStatus, MainModelCheckedAt: mainCheckedAt, VectorCheckedAt: vectorCheckedAt},
		Stats:  stats, Persona: persona, Groups: groups, Memories: memories,
		Relationships: relationships, Activity: activity, Retrieval: retrieval, ModelUsage: modelUsage, WindowMinutes: windowMinutes, WindowMetrics: windowMetrics,
	}, nil
}

func (d *Dashboard) loadCapabilityHealth(ctx context.Context) (string, string, *time.Time, *time.Time, error) {
	mainStatus := capabilityStatus(d.mainModelReady, "not_configured")
	vectorStatus := capabilityStatus(d.vectorSearchReady, "disabled")
	var mainCheckedAt, vectorCheckedAt sql.NullTime
	if d.health != nil {
		mainStatus, vectorStatus, mainChecked, vectorChecked := d.health.Snapshot()
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

func (d *Dashboard) loadLastTaskErrorAt(ctx context.Context) (*time.Time, error) {
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

func (d *Dashboard) loadWindowMetrics(ctx context.Context, groupID int64, windowMinutes int) (WindowMetrics, error) {
	var metrics WindowMetrics
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

func loadAdminMetricSeries(ctx context.Context, db *sql.DB, groupID int64, windowMinutes int) (MetricSeries, error) {
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
		return MetricSeries{}, fmt.Errorf("query metric series: %w", err)
	}
	defer rows.Close()
	points := make([]MetricPoint, 0, windowMinutes/5+2)
	for rows.Next() {
		var point MetricPoint
		if err := rows.Scan(&point.At, &point.Queries, &point.QueriesWithHits, &point.SelectedQueries,
			&point.Decisions, &point.Replies, &point.ModelCalls, &point.ModelErrors, &point.AvgDurationMS); err != nil {
			return MetricSeries{}, err
		}
		points = append(points, point)
	}
	if err := rows.Err(); err != nil {
		return MetricSeries{}, err
	}
	return MetricSeries{Points: points}, nil
}

func (d *Dashboard) loadModelUsageMetrics(ctx context.Context, groupID int64, windowMinutes int) (ModelUsageMetrics, error) {
	var metrics ModelUsageMetrics
	var avg sql.NullFloat64
	err := d.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(cached_tokens), 0),
		       COALESCE(SUM(cache_miss_tokens), 0), COALESCE(SUM(GREATEST(input_tokens - cached_tokens, 0)), 0),
		       COALESCE(SUM(output_tokens), 0), AVG(duration_ms),
		       COUNT(*) FILTER (WHERE error <> '')
		FROM model_usage_records
		WHERE created_at > NOW() - make_interval(mins => $2) AND ($1 = 0 OR group_id = $1)
	`, groupID, windowMinutes).Scan(&metrics.Calls, &metrics.InputTokens, &metrics.CachedTokens, &metrics.CacheMissTokens,
		&metrics.UncachedTokens, &metrics.OutputTokens, &avg, &metrics.ErrorCalls)
	if err != nil {
		return metrics, fmt.Errorf("model usage metrics: %w", err)
	}
	if avg.Valid {
		metrics.AvgDurationMS = avg.Float64
	}
	return metrics, nil
}

func (d *Dashboard) loadRetrievalMetrics(ctx context.Context, groupID int64, windowMinutes int) (RetrievalMetrics, error) {
	var metrics RetrievalMetrics
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

func (d *Dashboard) loadGroups(ctx context.Context) ([]Group, error) {
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
	byID := make(map[int64]Group)
	for rows.Next() {
		var group Group
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
			byID[groupID] = Group{GroupID: groupID}
		}
	}
	groupNames := d.loadGroupNames(ctx)
	groups := make([]Group, 0, len(byID))
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

func (d *Dashboard) loadGroupNames(ctx context.Context) map[int64]string {
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

func (d *Dashboard) loadStats(ctx context.Context) (Stats, error) {
	var stats Stats
	err := d.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(DISTINCT group_id) FROM messages WHERE group_id > 0),
			(SELECT COUNT(*) FROM member_profiles),
			(SELECT COUNT(*) FROM memories WHERE expires_at IS NULL OR expires_at > NOW()),
			(SELECT COUNT(*) FROM async_outbox WHERE status IN ('pending', 'running', 'retry'))
	`).Scan(&stats.Groups, &stats.Members, &stats.Memories, &stats.PendingTasks)
	return stats, err
}

func (d *Dashboard) loadPersona(ctx context.Context, groupID int64) (Persona, error) {
	runtimeFacts, err := d.facts.CurrentPersonaFacts(ctx, d.definition.Config.ID, time.Now())
	if err != nil {
		return Persona{}, fmt.Errorf("persona facts: %w", err)
	}
	view := personadomain.ResolveView(d.definition, runtimeFacts, time.Now())
	persona := Persona{
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
		return Persona{}, fmt.Errorf("persona state: %w", err)
	}
	runtime, err := d.state.GetRuntimeState(ctx, groupID)
	if err != nil {
		return Persona{}, fmt.Errorf("runtime state: %w", err)
	}
	persona.Mood, persona.Energy, persona.TalkBias, persona.Runtime = state.Mood, state.Energy, state.TalkBias, runtime
	return persona, nil
}

func (d *Dashboard) loadMemories(ctx context.Context, groupID int64) ([]Memory, error) {
	return loadAdminMemories(ctx, d.db, groupID, "active", "")
}

func loadAdminMemories(ctx context.Context, db *sql.DB, groupID int64, status, query string) ([]Memory, error) {
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
	memories := []Memory{}
	for rows.Next() {
		var memory Memory
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

func loadAdminMemoryPage(ctx context.Context, db *sql.DB, groupID int64, status, memoryType, query string, page, pageSize int) (MemoryPage, error) {
	where := ` FROM memories
		WHERE ($2 = 'all' OR ($2 = 'active' AND (expires_at IS NULL OR expires_at > NOW())) OR ($2 = 'expired' AND expires_at IS NOT NULL AND expires_at <= NOW()))
		  AND ($1 = 0 OR scope = 'global' OR scope = 'group:' || $1::text OR scope LIKE 'group:' || $1::text || ':user:%')
		  AND ($3 = '' OR type = $3)
		  AND ($4 = '' OR LOWER(subject || ' ' || content || ' ' || scope) LIKE '%' || LOWER($4) || '%')`
	args := []any{groupID, status, memoryType, strings.TrimSpace(query)}
	var total int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*)"+where, args...).Scan(&total); err != nil {
		return MemoryPage{}, fmt.Errorf("count memories: %w", err)
	}
	offset := (page - 1) * pageSize
	querySQL := `SELECT memory_id, scope, type, subject, content, confidence, importance, created_at, expires_at, source_event_id, updated_at` + where + fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	args = append(args, pageSize, offset)
	rows, err := db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return MemoryPage{}, fmt.Errorf("query memories: %w", err)
	}
	defer rows.Close()
	items := make([]Memory, 0, pageSize)
	for rows.Next() {
		var memory Memory
		var expires sql.NullTime
		if err := rows.Scan(&memory.ID, &memory.Scope, &memory.Type, &memory.Subject, &memory.Content,
			&memory.Confidence, &memory.Importance, &memory.CreatedAt, &expires, &memory.SourceEventID, &memory.UpdatedAt); err != nil {
			return MemoryPage{}, err
		}
		if expires.Valid {
			memory.ExpiresAt = &expires.Time
		}
		items = append(items, memory)
	}
	if err := rows.Err(); err != nil {
		return MemoryPage{}, err
	}
	return MemoryPage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func loadAdminMemes(ctx context.Context, db *sql.DB, groupID int64, query string) ([]Meme, error) {
	page, err := loadAdminMemePage(ctx, db, groupID, query, 1, 200, "")
	return page.Items, err
}

func loadAdminMemePage(ctx context.Context, db *sql.DB, groupID int64, query string, page, pageSize int, storagePath string) (MemePage, error) {
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
		return MemePage{}, fmt.Errorf("count memes: %w", err)
	}
	selectSQL := `SELECT a.meme_id, a.group_id, a.source_event_id, a.object_key, a.file_ext,
		       a.width, a.height, a.animated, a.status, a.send_count, a.dud_count,
		       a.created_at, a.last_sent_at, d.title, d.summary, d.keywords_json,
		       d.emotion_tags_json, d.scene_tags_json, d.confidence, d.reviewed` + where + fmt.Sprintf(" ORDER BY a.created_at DESC LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	offset := (page - 1) * pageSize
	args = append(args, pageSize, offset)
	rows, err := db.QueryContext(ctx, selectSQL, args...)
	if err != nil {
		return MemePage{}, fmt.Errorf("query memes: %w", err)
	}
	defer rows.Close()
	memes := make([]Meme, 0, pageSize)
	for rows.Next() {
		var meme Meme
		var keywords, emotions, scenes []byte
		if err := rows.Scan(&meme.MemeID, &meme.GroupID, &meme.SourceEventID, &meme.ObjectKey, &meme.FileExt,
			&meme.Width, &meme.Height, &meme.Animated, &meme.Status, &meme.SendCount, &meme.DudCount,
			&meme.CreatedAt, &meme.LastSentAt, &meme.Title, &meme.Summary, &keywords, &emotions, &scenes,
			&meme.Confidence, &meme.Reviewed); err != nil {
			return MemePage{}, err
		}
		meme.PreviewURL = memePreviewURL(storagePath, meme.ObjectKey)
		_ = json.Unmarshal(keywords, &meme.Keywords)
		_ = json.Unmarshal(emotions, &meme.EmotionTags)
		_ = json.Unmarshal(scenes, &meme.SceneTags)
		memes = append(memes, meme)
	}
	if err := rows.Err(); err != nil {
		return MemePage{}, err
	}
	return MemePage{Items: memes, Total: total, Page: page, PageSize: pageSize}, nil
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

func (d *Dashboard) loadRelationships(ctx context.Context, groupID int64) ([]Relationship, error) {
	page, err := loadAdminRelationshipPage(ctx, d.db, d.definition.Config.ID, groupID, "", 1, 100)
	return page.Items, err
}

func loadAdminRelationshipPage(ctx context.Context, db *sql.DB, personaID string, groupID int64, query string, page, pageSize int) (RelationshipPage, error) {
	where := ` FROM relationships r
		LEFT JOIN member_profiles p ON p.group_id = r.group_id AND p.user_id = r.user_id
		WHERE r.persona_id = $1 AND ($2 = 0 OR r.group_id = $2)
		  AND ($3 = '' OR LOWER(COALESCE(NULLIF(p.group_card, ''), NULLIF(p.nickname, ''), NULLIF(p.qq_nickname, ''), r.user_id::text) || ' ' || r.user_id::text) LIKE '%' || LOWER($3) || '%')`
	args := []any{personaID, groupID, strings.TrimSpace(query)}
	var total int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*)"+where, args...).Scan(&total); err != nil {
		return RelationshipPage{}, fmt.Errorf("count relationships: %w", err)
	}
	offset := (page - 1) * pageSize
	querySQL := `SELECT r.group_id, r.user_id,
		       COALESCE(NULLIF(p.group_card, ''), NULLIF(p.nickname, ''), NULLIF(p.qq_nickname, ''), r.user_id::text),
		       r.affinity, r.familiarity, r.tease_tolerance, r.trust, r.friction,
		       COALESCE(p.message_count, 0), r.last_interact_at` + where + fmt.Sprintf(" ORDER BY r.affinity DESC, r.last_interact_at DESC LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	args = append(args, pageSize, offset)
	rows, err := db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return RelationshipPage{}, fmt.Errorf("query relationships: %w", err)
	}
	defer rows.Close()
	relationships := make([]Relationship, 0, pageSize)
	for rows.Next() {
		var relationship Relationship
		if err := rows.Scan(&relationship.GroupID, &relationship.UserID, &relationship.Name,
			&relationship.Affinity, &relationship.Familiarity, &relationship.TeaseTolerance,
			&relationship.Trust, &relationship.Friction, &relationship.MessageCount, &relationship.LastInteractAt); err != nil {
			return RelationshipPage{}, err
		}
		relationships = append(relationships, relationship)
	}
	if err := rows.Err(); err != nil {
		return RelationshipPage{}, err
	}
	return RelationshipPage{Items: relationships, Total: total, Page: page, PageSize: pageSize}, nil
}

// loadRelationshipEvents 加载关系的事件历史
func loadRelationshipEvents(ctx context.Context, db *sql.DB, personaID string, groupID, userID int64) ([]RelationshipEvent, error) {
	query := `
		SELECT event_id, kind, valence, evidence_event_id, decision_id, created_at
		FROM relationship_events
		WHERE persona_id = $1 AND group_id = $2 AND user_id = $3
		ORDER BY created_at DESC
		LIMIT 100
	`
	rows, err := db.QueryContext(ctx, query, personaID, groupID, userID)
	if err != nil {
		return nil, fmt.Errorf("query relationship events: %w", err)
	}
	defer rows.Close()

	events := make([]RelationshipEvent, 0)
	for rows.Next() {
		var event RelationshipEvent
		var evidenceEventID, decisionID sql.NullString
		if err := rows.Scan(&event.EventID, &event.Kind, &event.Valence, &evidenceEventID, &decisionID, &event.CreatedAt); err != nil {
			return nil, err
		}
		event.EvidenceEventID = evidenceEventID.String
		event.DecisionID = decisionID.String
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

// loadRelationshipProjectionHistory 加载关系投影的变化历史
func loadRelationshipProjectionHistory(ctx context.Context, db *sql.DB, personaID string, groupID, userID int64) ([]ProjectionSnapshot, error) {
	query := `
		SELECT familiarity, affinity, trust, tease_tolerance, friction, revision,
		       trigger_event_id, trigger_kind, snapshot_at
		FROM relationship_history
		WHERE persona_id = $1 AND group_id = $2 AND user_id = $3
		ORDER BY revision DESC
		LIMIT 50
	`
	rows, err := db.QueryContext(ctx, query, personaID, groupID, userID)
	if err != nil {
		return nil, fmt.Errorf("query relationship history: %w", err)
	}
	defer rows.Close()

	snapshots := make([]ProjectionSnapshot, 0)
	for rows.Next() {
		var snapshot ProjectionSnapshot
		var triggerEventID, triggerKind sql.NullString
		if err := rows.Scan(
			&snapshot.Familiarity, &snapshot.Affinity, &snapshot.Trust,
			&snapshot.TeaseTolerance, &snapshot.Friction, &snapshot.Revision,
			&triggerEventID, &triggerKind, &snapshot.UpdatedAt,
		); err != nil {
			return nil, err
		}
		snapshot.TriggerEventID = triggerEventID.String
		snapshot.TriggerKind = triggerKind.String
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 如果历史表为空，降级查询当前状态
	if len(snapshots) == 0 {
		var snapshot ProjectionSnapshot
		err := db.QueryRowContext(ctx, `
			SELECT familiarity, affinity, trust, tease_tolerance, friction, revision, updated_at
			FROM relationships
			WHERE persona_id = $1 AND group_id = $2 AND user_id = $3
		`, personaID, groupID, userID).Scan(
			&snapshot.Familiarity, &snapshot.Affinity, &snapshot.Trust,
			&snapshot.TeaseTolerance, &snapshot.Friction, &snapshot.Revision, &snapshot.UpdatedAt,
		)
		if err == sql.ErrNoRows {
			return []ProjectionSnapshot{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("query current relationship: %w", err)
		}
		return []ProjectionSnapshot{snapshot}, nil
	}

	return snapshots, nil
}

func (d *Dashboard) loadActivity(ctx context.Context, groupID int64, windowMinutes int) ([]Activity, error) {
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
	activity := []Activity{}
	for rows.Next() {
		var item Activity
		if err := rows.Scan(&item.EventID, &item.At, &item.GroupID, &item.Type, &item.Label, &item.Subject, &item.Detail); err != nil {
			return nil, err
		}
		activity = append(activity, item)
	}
	return activity, rows.Err()
}

func loadAdminActivityPage(ctx context.Context, db *sql.DB, groupID int64, windowMinutes int, activityType string, page, pageSize int) (ActivityPage, error) {
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
		return ActivityPage{}, fmt.Errorf("count activity: %w", err)
	}
	var messageCount, decisionCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FILTER (WHERE type = 'message'), COUNT(*) FILTER (WHERE type = 'decision') FROM ("+source+") activity", windowMinutes, groupID).Scan(&messageCount, &decisionCount); err != nil {
		return ActivityPage{}, fmt.Errorf("count activity types: %w", err)
	}
	offset := (page - 1) * pageSize
	query := "SELECT event_id, at, group_id, type, label, subject, detail FROM (" + source + ") activity" + where + fmt.Sprintf(" ORDER BY at DESC LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	args = append(args, pageSize, offset)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return ActivityPage{}, fmt.Errorf("query activity: %w", err)
	}
	defer rows.Close()
	items := make([]Activity, 0, pageSize)
	for rows.Next() {
		var item Activity
		if err := rows.Scan(&item.EventID, &item.At, &item.GroupID, &item.Type, &item.Label, &item.Subject, &item.Detail); err != nil {
			return ActivityPage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ActivityPage{}, err
	}
	return ActivityPage{Items: items, Total: total, MessageCount: messageCount, DecisionCount: decisionCount, Page: page, PageSize: pageSize}, nil
}

func loadAdminEventDetail(ctx context.Context, db *sql.DB, eventID string) (EventDetail, error) {
	var detail EventDetail
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
		SELECT trace_id, query, candidate_count, hit_memory_ids_json, selected_memory_ids_json, outcome,
		       lexical_ranks_json, vector_ranks_json, candidate_scores_json, latency_ms, degraded_tracks_json, selection_reason, created_at
		FROM retrieval_traces WHERE event_id = $1 ORDER BY created_at ASC
	`, eventID)
	if err != nil {
		return detail, err
	}
	defer rows.Close()
	detail.Retrievals = []RetrievalDetail{}
	detail.ModelUsages = []ModelUsageDetail{}
	for rows.Next() {
		var item RetrievalDetail
		var hits, selected, lexicalRanks, vectorRanks, candidateScores, degradedTracks []byte
		if err := rows.Scan(&item.TraceID, &item.Query, &item.CandidateCount, &hits, &selected, &item.Outcome,
			&lexicalRanks, &vectorRanks, &candidateScores, &item.LatencyMS, &degradedTracks, &item.SelectionReason, &item.CreatedAt); err != nil {
			return detail, err
		}
		_ = json.Unmarshal(hits, &item.HitMemoryIDs)
		_ = json.Unmarshal(selected, &item.SelectedIDs)
		_ = json.Unmarshal(lexicalRanks, &item.LexicalRanks)
		_ = json.Unmarshal(vectorRanks, &item.VectorRanks)
		_ = json.Unmarshal(candidateScores, &item.CandidateScores)
		_ = json.Unmarshal(degradedTracks, &item.DegradedTracks)
		detail.Retrievals = append(detail.Retrievals, item)
	}
	if err := rows.Err(); err != nil {
		return detail, err
	}
	modelRows, err := db.QueryContext(ctx, `
			SELECT trace_id, iteration, input_tokens, cached_tokens, cache_miss_tokens, output_tokens, duration_ms,
			       tools_json, tool_calls_json, prompt_shape_json, usage_available, error, sent, final_action, drop_reason, created_at
		FROM model_usage_records WHERE event_id = $1
		ORDER BY created_at ASC
	`, eventID)
	if err == nil {
		defer modelRows.Close()
		for modelRows.Next() {
			var item ModelUsageDetail
			var tools, toolCalls, promptShape []byte
			if err := modelRows.Scan(&item.TraceID, &item.Iteration, &item.InputTokens, &item.CachedTokens, &item.CacheMissTokens,
				&item.OutputTokens, &item.DurationMS, &tools, &toolCalls, &promptShape, &item.UsageAvailable, &item.Error,
				&item.Sent, &item.FinalAction, &item.DropReason, &item.CreatedAt); err != nil {
				return detail, err
			}
			_ = json.Unmarshal(tools, &item.Tools)
			_ = json.Unmarshal(toolCalls, &item.ToolCalls)
			_ = json.Unmarshal(promptShape, &item.PromptShape)
			detail.ModelUsages = append(detail.ModelUsages, item)
		}
		if err := modelRows.Err(); err != nil {
			return detail, err
		}
	}
	var decision DecisionDetail
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

func setAdminEventDuration(detail *EventDetail) {
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
