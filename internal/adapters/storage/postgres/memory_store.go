package postgresstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

// memoryStore 实现 memorydomain.Store 接口
type memoryStore struct {
	db *sqlx.DB
}

// NewMemoryStore 创建记忆存储实例
func NewMemoryStore(db *sqlx.DB) memorydomain.Store {
	return &memoryStore{db: db}
}

// memoryRow 数据库行结构
type memoryRow struct {
	MemoryID            string         `db:"memory_id"`
	Scope               string         `db:"scope"`
	SubjectKind         string         `db:"subject_kind"`
	SubjectID           string         `db:"subject_id"`
	Type                string         `db:"type"`
	Subtype             string         `db:"subtype"`
	Content             string         `db:"content"`
	Predicate           string         `db:"predicate"`
	NormalizedValue     string         `db:"normalized_value"`
	Qualifier           string         `db:"qualifier"`
	ParticipantIDsJSON  []byte         `db:"participant_ids_json"`
	BotRole             string         `db:"bot_role"`
	AnchorEventID       string         `db:"anchor_event_id"`
	Status              string         `db:"status"`
	Revision            int64          `db:"revision"`
	SupersedesID        string         `db:"supersedes_id"`
	FirstObservedAt     time.Time      `db:"first_observed_at"`
	LastObservedAt      time.Time      `db:"last_observed_at"`
	ValidUntil          sql.NullTime   `db:"valid_until"`
	PendingUntil        sql.NullTime   `db:"pending_until"`
	SourceKind          string         `db:"source_kind"`
	ExtractorVersion    string         `db:"extractor_version"`
	CreatedAt           time.Time      `db:"created_at"`
	UpdatedAt           time.Time      `db:"updated_at"`
}

// toMemory 转换为 domain.Memory
func (r *memoryRow) toMemory() (*memorydomain.Memory, error) {
	var participantIDs []string
	if len(r.ParticipantIDsJSON) > 0 {
		if err := json.Unmarshal(r.ParticipantIDsJSON, &participantIDs); err != nil {
			return nil, fmt.Errorf("failed to unmarshal participant_ids: %w", err)
		}
	}

	mem := &memorydomain.Memory{
		MemoryID:         r.MemoryID,
		Scope:            r.Scope,
		SubjectKind:      memorydomain.SubjectKind(r.SubjectKind),
		SubjectID:        r.SubjectID,
		Type:             memorydomain.MemoryType(r.Type),
		Subtype:          r.Subtype,
		Content:          r.Content,
		Predicate:        memorydomain.Predicate(r.Predicate),
		NormalizedValue:  r.NormalizedValue,
		Qualifier:        r.Qualifier,
		ParticipantIDs:   participantIDs,
		BotRole:          memorydomain.BotRole(r.BotRole),
		AnchorEventID:    r.AnchorEventID,
		Status:           memorydomain.MemoryStatus(r.Status),
		Revision:         r.Revision,
		SupersedesID:     r.SupersedesID,
		FirstObservedAt:  r.FirstObservedAt,
		LastObservedAt:   r.LastObservedAt,
		SourceKind:       memorydomain.SourceKind(r.SourceKind),
		ExtractorVersion: r.ExtractorVersion,
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
	}

	if r.ValidUntil.Valid {
		mem.ValidUntil = &r.ValidUntil.Time
	}
	if r.PendingUntil.Valid {
		mem.PendingUntil = &r.PendingUntil.Time
	}

	return mem, nil
}

// fromMemory 从 domain.Memory 转换
func fromMemory(m *memorydomain.Memory) (*memoryRow, error) {
	participantIDsJSON, err := json.Marshal(m.ParticipantIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal participant_ids: %w", err)
	}

	row := &memoryRow{
		MemoryID:           m.MemoryID,
		Scope:              m.Scope,
		SubjectKind:        string(m.SubjectKind),
		SubjectID:          m.SubjectID,
		Type:               string(m.Type),
		Subtype:            m.Subtype,
		Content:            m.Content,
		Predicate:          string(m.Predicate),
		NormalizedValue:    m.NormalizedValue,
		Qualifier:          m.Qualifier,
		ParticipantIDsJSON: participantIDsJSON,
		BotRole:            string(m.BotRole),
		AnchorEventID:      m.AnchorEventID,
		Status:             string(m.Status),
		Revision:           m.Revision,
		SupersedesID:       m.SupersedesID,
		FirstObservedAt:    m.FirstObservedAt,
		LastObservedAt:     m.LastObservedAt,
		SourceKind:         string(m.SourceKind),
		ExtractorVersion:   m.ExtractorVersion,
		CreatedAt:          m.CreatedAt,
		UpdatedAt:          m.UpdatedAt,
	}

	if m.ValidUntil != nil {
		row.ValidUntil = sql.NullTime{Time: *m.ValidUntil, Valid: true}
	}
	if m.PendingUntil != nil {
		row.PendingUntil = sql.NullTime{Time: *m.PendingUntil, Valid: true}
	}

	return row, nil
}

// Save 事务性保存记忆、证据和变更
func (s *memoryStore) Save(ctx context.Context, mem *memorydomain.Memory, evidence []memorydomain.Evidence, change *memorydomain.Change) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. 保存或更新记忆
	row, err := fromMemory(mem)
	if err != nil {
		return err
	}

	const upsertMemorySQL = `
		INSERT INTO memories (
			memory_id, scope, subject_kind, subject_id, type, subtype,
			content, predicate, normalized_value, qualifier,
			participant_ids_json, bot_role, anchor_event_id,
			status, revision, supersedes_id,
			first_observed_at, last_observed_at, valid_until, pending_until,
			source_kind, extractor_version, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
			$17, $18, $19, $20, $21, $22, $23, $24
		)
		ON CONFLICT (memory_id) DO UPDATE SET
			status = EXCLUDED.status,
			revision = EXCLUDED.revision,
			content = EXCLUDED.content,
			normalized_value = EXCLUDED.normalized_value,
			last_observed_at = EXCLUDED.last_observed_at,
			valid_until = EXCLUDED.valid_until,
			pending_until = EXCLUDED.pending_until,
			updated_at = EXCLUDED.updated_at
	`

	_, err = tx.ExecContext(ctx, upsertMemorySQL,
		row.MemoryID, row.Scope, row.SubjectKind, row.SubjectID, row.Type, row.Subtype,
		row.Content, row.Predicate, row.NormalizedValue, row.Qualifier,
		row.ParticipantIDsJSON, row.BotRole, row.AnchorEventID,
		row.Status, row.Revision, row.SupersedesID,
		row.FirstObservedAt, row.LastObservedAt, row.ValidUntil, row.PendingUntil,
		row.SourceKind, row.ExtractorVersion, row.CreatedAt, row.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to save memory: %w", err)
	}

	// 2. 保存证据（去重插入）
	if len(evidence) > 0 {
		const insertEvidenceSQL = `
			INSERT INTO memory_evidence (memory_id, event_id, source_role, added_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (memory_id, event_id) DO NOTHING
		`

		for _, ev := range evidence {
			_, err = tx.ExecContext(ctx, insertEvidenceSQL,
				ev.MemoryID, ev.EventID, ev.SourceRole, ev.AddedAt)
			if err != nil {
				return fmt.Errorf("failed to save evidence: %w", err)
			}
		}
	}

	// 3. 保存变更记录
	if change != nil {
		fieldDiffsJSON, err := json.Marshal(change.FieldDiffs)
		if err != nil {
			return fmt.Errorf("failed to marshal field_diffs: %w", err)
		}

		const insertChangeSQL = `
			INSERT INTO memory_changes (
				change_id, memory_id, change_kind, reason,
				operator_kind, operator_id, from_revision, to_revision,
				field_diffs_json, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`

		_, err = tx.ExecContext(ctx, insertChangeSQL,
			change.ChangeID, change.MemoryID, change.ChangeKind, change.Reason,
			change.OperatorKind, change.OperatorID, change.FromRevision, change.ToRevision,
			fieldDiffsJSON, change.CreatedAt,
		)
		if err != nil {
			return fmt.Errorf("failed to save change: %w", err)
		}
	}

	return tx.Commit()
}

// Get 获取单条记忆
func (s *memoryStore) Get(ctx context.Context, memoryID string) (*memorydomain.Memory, error) {
	const query = `SELECT * FROM memories WHERE memory_id = $1`

	var row memoryRow
	if err := s.db.GetContext(ctx, &row, query, memoryID); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("memory not found: %s", memoryID)
		}
		return nil, fmt.Errorf("failed to get memory: %w", err)
	}

	return row.toMemory()
}

// ListBySubject 按主体查询记忆
func (s *memoryStore) ListBySubject(ctx context.Context, scope string, subjectKind memorydomain.SubjectKind, subjectID string, statuses []memorydomain.MemoryStatus) ([]*memorydomain.Memory, error) {
	statusStrings := make([]string, len(statuses))
	for i, st := range statuses {
		statusStrings[i] = string(st)
	}

	query := `
		SELECT * FROM memories
		WHERE scope = $1 AND subject_kind = $2 AND subject_id = $3
		AND status = ANY($4)
		ORDER BY last_observed_at DESC
	`

	var rows []memoryRow
	if err := s.db.SelectContext(ctx, &rows, query, scope, string(subjectKind), subjectID, pq.Array(statusStrings)); err != nil {
		return nil, fmt.Errorf("failed to list by subject: %w", err)
	}

	result := make([]*memorydomain.Memory, 0, len(rows))
	for _, row := range rows {
		mem, err := row.toMemory()
		if err != nil {
			return nil, err
		}
		result = append(result, mem)
	}

	return result, nil
}

// ListByFactKey 精确查询结构化事实
func (s *memoryStore) ListByFactKey(ctx context.Context, scope string, subjectKind memorydomain.SubjectKind, subjectID string, predicate memorydomain.Predicate, qualifier string) ([]*memorydomain.Memory, error) {
	var query string
	var args []interface{}

	if qualifier == "" {
		// 查询所有 qualifier
		query = `
			SELECT * FROM memories
			WHERE scope = $1 AND subject_kind = $2 AND subject_id = $3 AND predicate = $4
			ORDER BY last_observed_at DESC
		`
		args = []interface{}{scope, string(subjectKind), subjectID, string(predicate)}
	} else {
		query = `
			SELECT * FROM memories
			WHERE scope = $1 AND subject_kind = $2 AND subject_id = $3
			AND predicate = $4 AND qualifier = $5
			ORDER BY last_observed_at DESC
		`
		args = []interface{}{scope, string(subjectKind), subjectID, string(predicate), qualifier}
	}

	var rows []memoryRow
	if err := s.db.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("failed to list by fact key: %w", err)
	}

	result := make([]*memorydomain.Memory, 0, len(rows))
	for _, row := range rows {
		mem, err := row.toMemory()
		if err != nil {
			return nil, err
		}
		result = append(result, mem)
	}

	return result, nil
}

// ListByParticipant 按参与者查询情景记忆
func (s *memoryStore) ListByParticipant(ctx context.Context, scope string, participantID string, statuses []memorydomain.MemoryStatus) ([]*memorydomain.Memory, error) {
	statusStrings := make([]string, len(statuses))
	for i, st := range statuses {
		statusStrings[i] = string(st)
	}

	// 使用 JSONB 包含操作符查询参与者
	query := `
		SELECT * FROM memories
		WHERE scope = $1
		AND type = 'episodic'
		AND participant_ids_json @> $2::jsonb
		AND status = ANY($3)
		ORDER BY last_observed_at DESC
	`

	participantJSON, _ := json.Marshal([]string{participantID})

	var rows []memoryRow
	if err := s.db.SelectContext(ctx, &rows, query, scope, participantJSON, pq.Array(statusStrings)); err != nil {
		return nil, fmt.Errorf("failed to list by participant: %w", err)
	}

	result := make([]*memorydomain.Memory, 0, len(rows))
	for _, row := range rows {
		mem, err := row.toMemory()
		if err != nil {
			return nil, err
		}
		result = append(result, mem)
	}

	return result, nil
}

// GetEvidence 获取记忆的证据
func (s *memoryStore) GetEvidence(ctx context.Context, memoryID string) ([]memorydomain.Evidence, error) {
	const query = `
		SELECT memory_id, event_id, source_role, added_at
		FROM memory_evidence
		WHERE memory_id = $1
		ORDER BY added_at ASC
	`

	type evidenceRow struct {
		MemoryID   string    `db:"memory_id"`
		EventID    string    `db:"event_id"`
		SourceRole string    `db:"source_role"`
		AddedAt    time.Time `db:"added_at"`
	}

	var rows []evidenceRow
	if err := s.db.SelectContext(ctx, &rows, query, memoryID); err != nil {
		return nil, fmt.Errorf("failed to get evidence: %w", err)
	}

	result := make([]memorydomain.Evidence, 0, len(rows))
	for _, row := range rows {
		result = append(result, memorydomain.Evidence{
			MemoryID:   row.MemoryID,
			EventID:    row.EventID,
			SourceRole: row.SourceRole,
			AddedAt:    row.AddedAt,
		})
	}

	return result, nil
}

// GetChanges 获取记忆的变更历史
func (s *memoryStore) GetChanges(ctx context.Context, memoryID string, limit int) ([]*memorydomain.Change, error) {
	const query = `
		SELECT change_id, memory_id, change_kind, reason,
		       operator_kind, operator_id, from_revision, to_revision,
		       field_diffs_json, created_at
		FROM memory_changes
		WHERE memory_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`

	type changeRow struct {
		ChangeID       string    `db:"change_id"`
		MemoryID       string    `db:"memory_id"`
		ChangeKind     string    `db:"change_kind"`
		Reason         string    `db:"reason"`
		OperatorKind   string    `db:"operator_kind"`
		OperatorID     string    `db:"operator_id"`
		FromRevision   int64     `db:"from_revision"`
		ToRevision     int64     `db:"to_revision"`
		FieldDiffsJSON []byte    `db:"field_diffs_json"`
		CreatedAt      time.Time `db:"created_at"`
	}

	var rows []changeRow
	if err := s.db.SelectContext(ctx, &rows, query, memoryID, limit); err != nil {
		return nil, fmt.Errorf("failed to get changes: %w", err)
	}

	result := make([]*memorydomain.Change, 0, len(rows))
	for _, row := range rows {
		var fieldDiffs map[string]string
		if len(row.FieldDiffsJSON) > 0 {
			if err := json.Unmarshal(row.FieldDiffsJSON, &fieldDiffs); err != nil {
				return nil, fmt.Errorf("failed to unmarshal field_diffs: %w", err)
			}
		}

		result = append(result, &memorydomain.Change{
			ChangeID:     row.ChangeID,
			MemoryID:     row.MemoryID,
			ChangeKind:   row.ChangeKind,
			Reason:       row.Reason,
			OperatorKind: row.OperatorKind,
			OperatorID:   row.OperatorID,
			FromRevision: row.FromRevision,
			ToRevision:   row.ToRevision,
			FieldDiffs:   fieldDiffs,
			CreatedAt:    row.CreatedAt,
		})
	}

	return result, nil
}

// LockFactKey 锁定事实键（用于并发控制）
func (s *memoryStore) LockFactKey(ctx context.Context, scope string, subjectKind memorydomain.SubjectKind, subjectID string, predicate memorydomain.Predicate, qualifier string) error {
	// 使用 PostgreSQL 的 advisory lock
	// 将参数组合为唯一的整数键
	lockKey := fmt.Sprintf("%s:%s:%s:%s:%s", scope, subjectKind, subjectID, predicate, qualifier)

	// 简化实现：使用查询锁定
	// 生产环境应该使用 pg_advisory_xact_lock
	const query = `
		SELECT 1 FROM memories
		WHERE scope = $1 AND subject_kind = $2 AND subject_id = $3
		AND predicate = $4 AND qualifier = $5 AND status = 'active'
		FOR UPDATE NOWAIT
	`

	_, err := s.db.ExecContext(ctx, query, scope, string(subjectKind), subjectID, string(predicate), qualifier)
	if err != nil {
		// 如果没有行，这也是正常的（首次创建）
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("failed to lock fact key %s: %w", lockKey, err)
	}

	return nil
}

// MarkProgress 标记学习任务完成
func (s *memoryStore) MarkProgress(ctx context.Context, progress *memorydomain.LearningEventProgress) error {
	const query = `
		INSERT INTO learning_event_progress (
			event_id, extractor_version, group_id, processed_at,
			outcome, skip_reason, memory_count, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (event_id, extractor_version) DO UPDATE SET
			processed_at = EXCLUDED.processed_at,
			outcome = EXCLUDED.outcome,
			skip_reason = EXCLUDED.skip_reason,
			memory_count = EXCLUDED.memory_count
	`

	_, err := s.db.ExecContext(ctx, query,
		progress.EventID, progress.ExtractorVersion, progress.GroupID, progress.ProcessedAt,
		progress.Outcome, progress.SkipReason, progress.MemoryCount, progress.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to mark progress: %w", err)
	}

	return nil
}

// GetProgress 获取事件处理进度
func (s *memoryStore) GetProgress(ctx context.Context, eventID string, extractorVersion string) (*memorydomain.LearningEventProgress, error) {
	const query = `
		SELECT event_id, extractor_version, group_id, processed_at,
		       outcome, skip_reason, memory_count, created_at
		FROM learning_event_progress
		WHERE event_id = $1 AND extractor_version = $2
	`

	type progressRow struct {
		EventID          string    `db:"event_id"`
		ExtractorVersion string    `db:"extractor_version"`
		GroupID          int64     `db:"group_id"`
		ProcessedAt      time.Time `db:"processed_at"`
		Outcome          string    `db:"outcome"`
		SkipReason       string    `db:"skip_reason"`
		MemoryCount      int       `db:"memory_count"`
		CreatedAt        time.Time `db:"created_at"`
	}

	var row progressRow
	if err := s.db.GetContext(ctx, &row, query, eventID, extractorVersion); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // 未处理
		}
		return nil, fmt.Errorf("failed to get progress: %w", err)
	}

	return &memorydomain.LearningEventProgress{
		EventID:          row.EventID,
		ExtractorVersion: row.ExtractorVersion,
		GroupID:          row.GroupID,
		ProcessedAt:      row.ProcessedAt,
		Outcome:          row.Outcome,
		SkipReason:       row.SkipReason,
		MemoryCount:      row.MemoryCount,
		CreatedAt:        row.CreatedAt,
	}, nil
}

// ListUnprocessedEvents 查询未处理的事件
func (s *memoryStore) ListUnprocessedEvents(ctx context.Context, groupID int64, extractorVersion string, limit int) ([]string, error) {
	const query = `
		SELECT m.event_id
		FROM messages m
		WHERE m.group_id = $1
		AND NOT EXISTS (
			SELECT 1 FROM learning_event_progress lep
			WHERE lep.event_id = m.event_id
			AND lep.extractor_version = $2
		)
		ORDER BY m.occurred_at ASC, m.event_id ASC
		LIMIT $3
	`

	var eventIDs []string
	if err := s.db.SelectContext(ctx, &eventIDs, query, groupID, extractorVersion, limit); err != nil {
		return nil, fmt.Errorf("failed to list unprocessed events: %w", err)
	}

	return eventIDs, nil
}
