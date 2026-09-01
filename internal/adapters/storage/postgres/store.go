package postgresstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/phlin/go-agent/internal/application/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
	searchcore "github.com/phlin/go-agent/internal/search"
	"github.com/phlin/go-agent/internal/search/bm25"
)

var (
	_ ports.MemoryStore               = (*Store)(nil)
	_ ports.LearningStateStore        = (*Store)(nil)
	_ ports.ThoughtStore              = (*Store)(nil)
	_ ports.MemeStore                 = (*Store)(nil)
	_ ports.ProfileStore              = (*Store)(nil)
	_ ports.PersonaFactStore          = (*Store)(nil)
	_ ports.OutboxStore               = (*Store)(nil)
	_ ports.AtomicMemeProjectionStore = (*Store)(nil)
)

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return db, nil
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) EnqueueOutbox(ctx context.Context, task ports.OutboxTask) error {
	if task.Kind == "" || task.IdempotencyKey == "" || task.ID == "" {
		return errors.New("outbox: id, kind and idempotency key are required")
	}
	if task.Status == "" {
		task.Status = ports.OutboxPending
	}
	if task.AvailableAt.IsZero() {
		task.AvailableAt = time.Now()
	}
	if task.MaxAttempts <= 0 {
		task.MaxAttempts = 5
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now()
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO async_outbox (
			task_id, kind, idempotency_key, payload_json, status, attempts, max_attempts,
			available_at, locked_until, locked_by, last_error, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULL, NULL, NULL, $9, $10)
		ON CONFLICT (kind, idempotency_key) DO UPDATE SET task_id = async_outbox.task_id
	`, task.ID, task.Kind, task.IdempotencyKey, task.Payload, task.Status, task.Attempts, task.MaxAttempts,
		task.AvailableAt, task.CreatedAt, task.UpdatedAt)
	return err
}

func (s *Store) ClaimOutbox(ctx context.Context, workerID string, now time.Time, lease time.Duration, limit int) ([]ports.OutboxTask, error) {
	if workerID == "" {
		return nil, errors.New("outbox: worker id is required")
	}
	if lease <= 0 {
		lease = time.Minute
	}
	if limit <= 0 {
		limit = 1
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT task_id, kind, idempotency_key, payload_json, status, attempts, max_attempts,
		       available_at, locked_until, locked_by, last_error, created_at, updated_at
		FROM async_outbox
		WHERE (status IN ($1, $2) AND available_at <= $3)
		   OR (status = $4 AND (locked_until IS NULL OR locked_until <= $5))
		ORDER BY created_at ASC, task_id ASC
		LIMIT $6
		FOR UPDATE SKIP LOCKED
	`, ports.OutboxPending, ports.OutboxRetry, now, ports.OutboxRunning, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// ponytail: pgx 不允许在同一事务连接上边遍历 rows 边 Exec,先收集完再统一加锁,
	// 行数受 LIMIT 约束,内存可控。
	tasks := make([]ports.OutboxTask, 0, limit)
	for rows.Next() {
		var task ports.OutboxTask
		var lockedUntil sql.NullTime
		var lockedBy, lastError sql.NullString
		if err := rows.Scan(&task.ID, &task.Kind, &task.IdempotencyKey, &task.Payload, &task.Status, &task.Attempts, &task.MaxAttempts,
			&task.AvailableAt, &lockedUntil, &lockedBy, &lastError, &task.CreatedAt, &task.UpdatedAt); err != nil {
			return nil, err
		}
		if lockedUntil.Valid {
			task.LockedUntil = lockedUntil.Time
		}
		task.LockedBy = lockedBy.String
		task.LastError = lastError.String
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	for i := range tasks {
		tasks[i].Status = ports.OutboxRunning
		tasks[i].Attempts++
		tasks[i].LockedBy = workerID
		tasks[i].LockedUntil = now.Add(lease)
		tasks[i].UpdatedAt = now
		if _, err := tx.ExecContext(ctx, `UPDATE async_outbox SET status = $1, attempts = $2, locked_until = $3, locked_by = $4, updated_at = $5 WHERE task_id = $6`,
			ports.OutboxRunning, tasks[i].Attempts, tasks[i].LockedUntil, workerID, now, tasks[i].ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *Store) CompleteOutbox(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE async_outbox SET status = $1, locked_until = NULL, locked_by = NULL, updated_at = $2 WHERE task_id = $3`, ports.OutboxCompleted, time.Now(), id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errors.New("outbox: task not found")
	}
	return nil
}

func (s *Store) FailOutbox(ctx context.Context, id string, taskErr error, retryAt time.Time) error {
	var attempts, maxAttempts int
	if err := s.db.QueryRowContext(ctx, `SELECT attempts, max_attempts FROM async_outbox WHERE task_id = $1`, id).Scan(&attempts, &maxAttempts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("outbox: task not found")
		}
		return err
	}
	status := ports.OutboxRetry
	if retryAt.IsZero() || (maxAttempts > 0 && attempts >= maxAttempts) {
		status = ports.OutboxDeadLetter
	}
	var retry any
	if status == ports.OutboxRetry {
		retry = retryAt
	}
	_, err := s.db.ExecContext(ctx, `UPDATE async_outbox SET status = $1, available_at = COALESCE($2, available_at), locked_until = NULL, locked_by = NULL, last_error = $3, updated_at = $4 WHERE task_id = $5`,
		status, retry, nullableError(taskErr), time.Now(), id)
	return err
}

func nullableError(err error) any {
	if err == nil {
		return nil
	}
	return err.Error()
}

func (s *Store) ArchiveEvent(ctx context.Context, event conversationdomain.ConversationEvent) error {
	segmentsJSON, err := json.Marshal(event.Segments)
	if err != nil {
		return err
	}
	attachmentsJSON, err := json.Marshal(event.Attachments)
	if err != nil {
		return err
	}

	occurredAt := time.Unix(event.TimestampUnix, 0)
	if event.TimestampUnix == 0 {
		occurredAt = time.Now()
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO messages (
			event_id, group_id, user_id, message_id, reply_to_message_id, kind, text_content,
			segments_json, attachments_json, mentioned_bot, named_bot, is_reply_to_bot,
			occurred_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (event_id) DO UPDATE SET
			text_content = EXCLUDED.text_content,
			segments_json = EXCLUDED.segments_json,
			attachments_json = EXCLUDED.attachments_json,
			mentioned_bot = EXCLUDED.mentioned_bot,
			named_bot = EXCLUDED.named_bot,
			is_reply_to_bot = EXCLUDED.is_reply_to_bot,
			occurred_at = EXCLUDED.occurred_at
	`, event.EventID, event.GroupID, event.UserID, event.MessageID, nullableString(event.ReplyToMessageID), event.Kind,
		event.Text, segmentsJSON, attachmentsJSON, event.MentionedBot, event.NamedBot, event.IsReplyToBot, occurredAt, time.Now())
	return err
}

func (s *Store) RecentEvents(ctx context.Context, groupID int64, limit int) ([]conversationdomain.ConversationEvent, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, group_id, user_id, message_id, reply_to_message_id, kind, text_content,
		       segments_json, attachments_json, mentioned_bot, named_bot, is_reply_to_bot, occurred_at
		FROM messages
		WHERE group_id = $1
		ORDER BY occurred_at DESC
		LIMIT $2
	`, groupID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []conversationdomain.ConversationEvent{}
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	slices.Reverse(events)
	return events, nil
}

func (s *Store) EventsAfter(ctx context.Context, groupID int64, after time.Time, afterEventID string, limit int) ([]conversationdomain.ConversationEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, group_id, user_id, message_id, reply_to_message_id, kind, text_content,
		       segments_json, attachments_json, mentioned_bot, named_bot, is_reply_to_bot, occurred_at
		FROM messages
		WHERE group_id = $1 AND (occurred_at > $2 OR (occurred_at = $3 AND event_id > $4))
		ORDER BY occurred_at ASC, event_id ASC
		LIMIT $5
	`, groupID, after, after, afterEventID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []conversationdomain.ConversationEvent{}
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// scanEvent 从单行扫描 ConversationEvent,RecentEvents/EventsAfter 共用。
func scanEvent(rows *sql.Rows) (conversationdomain.ConversationEvent, error) {
	var (
		event           conversationdomain.ConversationEvent
		replyTo         sql.NullString
		segmentsJSON    []byte
		attachmentsJSON []byte
		occurredAt      time.Time
		kind            string
	)
	if err := rows.Scan(
		&event.EventID,
		&event.GroupID,
		&event.UserID,
		&event.MessageID,
		&replyTo,
		&kind,
		&event.Text,
		&segmentsJSON,
		&attachmentsJSON,
		&event.MentionedBot,
		&event.NamedBot,
		&event.IsReplyToBot,
		&occurredAt,
	); err != nil {
		return conversationdomain.ConversationEvent{}, err
	}
	event.Kind = conversationdomain.EventKind(kind)
	event.ReplyToMessageID = replyTo.String
	event.TimestampUnix = occurredAt.Unix()
	if err := json.Unmarshal(segmentsJSON, &event.Segments); err != nil {
		return conversationdomain.ConversationEvent{}, err
	}
	if err := json.Unmarshal(attachmentsJSON, &event.Attachments); err != nil {
		return conversationdomain.ConversationEvent{}, err
	}
	return event, nil
}

func (s *Store) UpsertMemory(ctx context.Context, record memorydomain.MemoryRecord) error {
	return upsertMemoryExec(ctx, s.db, record)
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func upsertMemoryExec(ctx context.Context, execer sqlExecer, record memorydomain.MemoryRecord) error {
	now := time.Now()
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	revision := record.Revision
	if revision <= 0 {
		revision = createdAt.UnixNano()
	}

	_, err := execer.ExecContext(ctx, `
		INSERT INTO memories (
			memory_id, scope, type, subject, content, source_event_id, descriptor_ref,
			confidence, importance, revision, created_at, expires_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (memory_id) DO UPDATE SET
			scope = EXCLUDED.scope,
			type = EXCLUDED.type,
			subject = EXCLUDED.subject,
			content = EXCLUDED.content,
			source_event_id = EXCLUDED.source_event_id,
			descriptor_ref = EXCLUDED.descriptor_ref,
			confidence = EXCLUDED.confidence,
			importance = EXCLUDED.importance,
			revision = EXCLUDED.revision,
			expires_at = EXCLUDED.expires_at,
			updated_at = EXCLUDED.updated_at
	`, record.MemoryID, record.Scope, record.Type, record.Subject, record.Content, record.SourceEventID, record.DescriptorRef,
		record.Confidence, record.Importance, revision, createdAt, nullableTime(record.ExpiresAt), now)
	return err
}

// UpsertMemoryAndEnqueueVector makes the memory fact and its projection task
// durable together. The task remains replayable if embedding is unavailable.
func (s *Store) UpsertMemoryAndEnqueueVector(ctx context.Context, record memorydomain.MemoryRecord, task ports.OutboxTask) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := upsertMemoryExec(ctx, tx, record); err != nil {
		return err
	}
	if task.Status == "" {
		task.Status = ports.OutboxPending
	}
	if task.AvailableAt.IsZero() {
		task.AvailableAt = time.Now()
	}
	if task.MaxAttempts <= 0 {
		task.MaxAttempts = 5
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now()
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO async_outbox (
			task_id, kind, idempotency_key, payload_json, status, attempts, max_attempts,
			available_at, locked_until, locked_by, last_error, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULL, NULL, NULL, $9, $10)
		ON CONFLICT (kind, idempotency_key) DO UPDATE SET
			payload_json = EXCLUDED.payload_json,
			status = CASE WHEN async_outbox.status = $11 THEN async_outbox.status ELSE EXCLUDED.status END,
			available_at = CASE WHEN async_outbox.status = $11 THEN async_outbox.available_at ELSE EXCLUDED.available_at END,
			updated_at = EXCLUDED.updated_at
	`, task.ID, task.Kind, task.IdempotencyKey, task.Payload, task.Status, task.Attempts, task.MaxAttempts,
		task.AvailableAt, task.CreatedAt, task.UpdatedAt, ports.OutboxRunning); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) QueryMemories(ctx context.Context, query ports.MemoryQuery) ([]memorydomain.MemoryRecord, error) {
	base := `
		SELECT memory_id, scope, type, subject, content, source_event_id, descriptor_ref,
		       confidence, importance, revision, created_at, expires_at
		FROM memories
		WHERE 1=1
	`
	args := []any{}
	trimmedQuery := strings.TrimSpace(query.Query)
	if query.Scope != "" {
		if !searchcore.MemoryScopeVisible(query.Scope, query.GroupID, query.UserID) {
			return []memorydomain.MemoryRecord{}, nil
		}
		base += " AND scope = $" + strconv.Itoa(len(args)+1)
		args = append(args, query.Scope)
	} else if query.GroupID != 0 {
		base += " AND (scope = $" + strconv.Itoa(len(args)+1) + " OR scope = $" + strconv.Itoa(len(args)+2) + " OR scope = 'global')"
		args = append(args, fmt.Sprintf("group:%d", query.GroupID), fmt.Sprintf("group:%d:user:%d", query.GroupID, query.UserID))
	} else {
		base += " AND scope = 'global'"
	}
	if len(query.Types) > 0 {
		phs := make([]string, 0, len(query.Types))
		for _, memoryType := range query.Types {
			args = append(args, memoryType)
			phs = append(phs, "$"+strconv.Itoa(len(args)))
		}
		base += " AND type IN (" + strings.Join(phs, ",") + ")"
	}
	// 遗忘：过期记忆不再召回；重要性按时间贴现（高重要性衰减更慢，
	// 半衰 = importance*30 天）——重要旧事压过无聊近事，无聊近事先淡出。
	base += " AND (expires_at IS NULL OR expires_at > NOW())"
	if trimmedQuery == "" {
		base += " ORDER BY importance / (1 + EXTRACT(EPOCH FROM (NOW() - created_at)) / 86400.0 / (GREATEST(importance, 0.1) * 30.0)) DESC, created_at DESC"
		if query.TopK > 0 {
			base += " LIMIT $" + strconv.Itoa(len(args)+1)
			args = append(args, query.TopK)
		}
	}

	rows, err := s.db.QueryContext(ctx, base, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := []memorydomain.MemoryRecord{}
	for rows.Next() {
		var (
			record    memorydomain.MemoryRecord
			expiresAt sql.NullTime
		)
		if err := rows.Scan(
			&record.MemoryID,
			&record.Scope,
			&record.Type,
			&record.Subject,
			&record.Content,
			&record.SourceEventID,
			&record.DescriptorRef,
			&record.Confidence,
			&record.Importance,
			&record.Revision,
			&record.CreatedAt,
			&expiresAt,
		); err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			record.ExpiresAt = &expiresAt.Time
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if trimmedQuery == "" || len(records) == 0 {
		return records, nil
	}
	documents := make([]bm25.Document, len(records))
	byID := make(map[string]memorydomain.MemoryRecord, len(records))
	for i, record := range records {
		documents[i] = bm25.Document{ID: record.MemoryID, Text: record.Subject + "\n" + record.Content}
		byID[record.MemoryID] = record
	}
	ranked := bm25.Rank(trimmedQuery, documents, query.TopK)
	result := make([]memorydomain.MemoryRecord, 0, len(ranked))
	for _, item := range ranked {
		result = append(result, byID[item.ID])
	}
	return result, nil
}

func (s *Store) GetMemberProfile(ctx context.Context, groupID, userID int64) (profiledomain.MemberProfile, error) {
	var (
		profile           profiledomain.MemberProfile
		tagsJSON          []byte
		commonPhrasesJSON []byte
		interestsJSON     []byte
		traitsJSON        []byte
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT nickname, message_count, last_spoke_at, active_score, tags_json, common_phrases_json, interests_json, traits_json
		FROM member_profiles
		WHERE group_id = $1 AND user_id = $2
	`, groupID, userID).Scan(
		&profile.Stats.Nickname,
		&profile.Stats.MessageCount,
		&profile.Stats.LastSpokeAt,
		&profile.Stats.ActiveScore,
		&tagsJSON,
		&commonPhrasesJSON,
		&interestsJSON,
		&traitsJSON,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return profiledomain.MemberProfile{Stats: profiledomain.MemberStats{GroupID: groupID, UserID: userID}}, nil
		}
		return profiledomain.MemberProfile{}, err
	}
	profile.Stats.GroupID = groupID
	profile.Stats.UserID = userID
	_ = json.Unmarshal(tagsJSON, &profile.Tags)
	_ = json.Unmarshal(commonPhrasesJSON, &profile.CommonPhrases)
	_ = json.Unmarshal(interestsJSON, &profile.Interests)
	_ = json.Unmarshal(traitsJSON, &profile.Traits)
	return profile, nil
}

func (s *Store) SaveMemberProfile(ctx context.Context, profile profiledomain.MemberProfile) error {
	tagsJSON, _ := json.Marshal(profile.Tags)
	commonPhrasesJSON, _ := json.Marshal(profile.CommonPhrases)
	interestsJSON, _ := json.Marshal(profile.Interests)
	traitsJSON, _ := json.Marshal(profile.Traits)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO member_profiles (
			group_id, user_id, nickname, message_count, last_spoke_at, active_score,
			tags_json, common_phrases_json, interests_json, traits_json, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (group_id, user_id) DO UPDATE SET
			nickname = EXCLUDED.nickname,
			message_count = EXCLUDED.message_count,
			last_spoke_at = EXCLUDED.last_spoke_at,
			active_score = EXCLUDED.active_score,
			tags_json = EXCLUDED.tags_json,
			common_phrases_json = EXCLUDED.common_phrases_json,
			interests_json = EXCLUDED.interests_json,
			traits_json = EXCLUDED.traits_json,
			updated_at = EXCLUDED.updated_at
	`, profile.Stats.GroupID, profile.Stats.UserID, profile.Stats.Nickname, profile.Stats.MessageCount,
		coalesceTime(profile.Stats.LastSpokeAt), profile.Stats.ActiveScore, tagsJSON, commonPhrasesJSON, interestsJSON, traitsJSON, time.Now())
	return err
}

func (s *Store) GetRelationship(ctx context.Context, personaID string, groupID, userID int64) (profiledomain.RelationshipState, error) {
	var state profiledomain.RelationshipState
	err := s.db.QueryRowContext(ctx, `
		SELECT familiarity, affinity, tease_tolerance, grudge_score, last_interact_at
		FROM relationships
		WHERE persona_id = $1 AND group_id = $2 AND user_id = $3
	`, personaID, groupID, userID).Scan(
		&state.Familiarity,
		&state.Affinity,
		&state.TeaseTolerance,
		&state.GrudgeScore,
		&state.LastInteractAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return profiledomain.RelationshipState{
				PersonaID: personaID,
				GroupID:   groupID,
				UserID:    userID,
			}, nil
		}
		return profiledomain.RelationshipState{}, err
	}
	state.PersonaID = personaID
	state.GroupID = groupID
	state.UserID = userID
	return state, nil
}

func (s *Store) SaveRelationship(ctx context.Context, state profiledomain.RelationshipState) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO relationships (
			persona_id, group_id, user_id, familiarity, affinity, tease_tolerance, grudge_score, last_interact_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (persona_id, group_id, user_id) DO UPDATE SET
			familiarity = EXCLUDED.familiarity,
			affinity = EXCLUDED.affinity,
			tease_tolerance = EXCLUDED.tease_tolerance,
			grudge_score = EXCLUDED.grudge_score,
			last_interact_at = EXCLUDED.last_interact_at
	`, state.PersonaID, state.GroupID, state.UserID, state.Familiarity, state.Affinity, state.TeaseTolerance, state.GrudgeScore, coalesceTime(state.LastInteractAt))
	return err
}

func (s *Store) UpsertMeme(ctx context.Context, asset mediadomain.MemeAsset, descriptor mediadomain.MemeDescriptor) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if err = upsertMemeExec(ctx, tx, asset, descriptor); err != nil {
		return err
	}
	return tx.Commit()
}

// UpsertMemeAndEnqueueVector makes the meme fact and its vector projection
// task durable together. The outbox task is replayable if embedding is down.
func (s *Store) UpsertMemeAndEnqueueVector(ctx context.Context, asset mediadomain.MemeAsset, descriptor mediadomain.MemeDescriptor, task ports.OutboxTask) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := upsertMemeExec(ctx, tx, asset, descriptor); err != nil {
		return err
	}
	if task.Status == "" {
		task.Status = ports.OutboxPending
	}
	if task.AvailableAt.IsZero() {
		task.AvailableAt = time.Now()
	}
	if task.MaxAttempts <= 0 {
		task.MaxAttempts = 5
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now()
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO async_outbox (
			task_id, kind, idempotency_key, payload_json, status, attempts, max_attempts,
			available_at, locked_until, locked_by, last_error, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULL, NULL, NULL, $9, $10)
		ON CONFLICT (kind, idempotency_key) DO UPDATE SET
			payload_json = EXCLUDED.payload_json,
			status = CASE WHEN async_outbox.status = $11 THEN async_outbox.status ELSE EXCLUDED.status END,
			available_at = CASE WHEN async_outbox.status = $11 THEN async_outbox.available_at ELSE EXCLUDED.available_at END,
			updated_at = EXCLUDED.updated_at
	`, task.ID, task.Kind, task.IdempotencyKey, task.Payload, task.Status, task.Attempts, task.MaxAttempts,
		task.AvailableAt, task.CreatedAt, task.UpdatedAt, ports.OutboxRunning); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertMemeExec(ctx context.Context, execer sqlExecer, asset mediadomain.MemeAsset, descriptor mediadomain.MemeDescriptor) error {
	createdAt := coalesceTime(asset.CreatedAt)
	revision := asset.Revision
	if revision <= 0 {
		revision = createdAt.UnixNano()
	}
	if _, err := execer.ExecContext(ctx, `
		INSERT INTO meme_assets (
			meme_id, group_id, source_event_id, object_key, file_ext, content_hash, perceptual_hash,
			width, height, animated, status, revision, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (meme_id) DO UPDATE SET
			group_id = EXCLUDED.group_id,
			source_event_id = EXCLUDED.source_event_id,
			object_key = EXCLUDED.object_key,
			file_ext = EXCLUDED.file_ext,
			content_hash = EXCLUDED.content_hash,
			perceptual_hash = EXCLUDED.perceptual_hash,
			width = EXCLUDED.width,
			height = EXCLUDED.height,
			animated = EXCLUDED.animated,
			status = EXCLUDED.status,
			revision = EXCLUDED.revision
	`, asset.MemeID, asset.GroupID, asset.SourceEventID, asset.ObjectKey, asset.FileExt, asset.ContentHash, asset.PerceptualHash,
		asset.Width, asset.Height, asset.Animated, asset.Status, revision, createdAt); err != nil {
		return err
	}

	keywordsJSON, _ := json.Marshal(descriptor.Keywords)
	emotionJSON, _ := json.Marshal(descriptor.EmotionTags)
	sceneJSON, _ := json.Marshal(descriptor.SceneTags)
	usageJSON, _ := json.Marshal(descriptor.UsageHints)
	if _, err := execer.ExecContext(ctx, `
		INSERT INTO meme_descriptors (
			meme_id, title, summary, keywords_json, emotion_tags_json, scene_tags_json,
			usage_hints_json, language, confidence, reviewed, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (meme_id) DO UPDATE SET
			title = EXCLUDED.title,
			summary = EXCLUDED.summary,
			keywords_json = EXCLUDED.keywords_json,
			emotion_tags_json = EXCLUDED.emotion_tags_json,
			scene_tags_json = EXCLUDED.scene_tags_json,
			usage_hints_json = EXCLUDED.usage_hints_json,
			language = EXCLUDED.language,
			confidence = EXCLUDED.confidence,
			reviewed = EXCLUDED.reviewed,
			updated_at = EXCLUDED.updated_at
	`, descriptor.MemeID, descriptor.Title, descriptor.Summary, keywordsJSON, emotionJSON, sceneJSON,
		usageJSON, descriptor.Language, descriptor.Confidence, descriptor.Reviewed, coalesceTime(descriptor.UpdatedAt)); err != nil {
		return err
	}
	return nil
}

func (s *Store) SearchMemes(ctx context.Context, query ports.MemeQuery) ([]mediadomain.MemeSearchResult, error) {
	trimmedQuery := strings.TrimSpace(query.Query)
	limit := query.TopK
	if limit <= 0 {
		limit = 5
	}

	base := `
		SELECT a.meme_id, d.title, d.summary, d.keywords_json, d.emotion_tags_json, d.scene_tags_json,
		       d.usage_hints_json, d.language, d.confidence, d.reviewed
		FROM meme_assets a
		JOIN meme_descriptors d ON d.meme_id = a.meme_id
		WHERE a.status = 'approved'
		  AND (a.group_id = $1 OR a.group_id = 0)`
	args := []any{query.GroupID}
	if trimmedQuery == "" {
		base += " ORDER BY a.send_count DESC, d.updated_at DESC LIMIT $2"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, base, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []mediadomain.MemeSearchResult{}
	for rows.Next() {
		var (
			result       mediadomain.MemeSearchResult
			keywordsJSON []byte
			emotionJSON  []byte
			sceneJSON    []byte
			usageJSON    []byte
			descriptor   mediadomain.MemeDescriptor
		)
		if err := rows.Scan(
			&result.MemeID,
			&descriptor.Title,
			&descriptor.Summary,
			&keywordsJSON,
			&emotionJSON,
			&sceneJSON,
			&usageJSON,
			&descriptor.Language,
			&descriptor.Confidence,
			&descriptor.Reviewed,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(keywordsJSON, &descriptor.Keywords)
		_ = json.Unmarshal(emotionJSON, &descriptor.EmotionTags)
		_ = json.Unmarshal(sceneJSON, &descriptor.SceneTags)
		_ = json.Unmarshal(usageJSON, &descriptor.UsageHints)
		descriptor.MemeID = result.MemeID
		result.Score = descriptor.Confidence
		result.MatchType = "keyword"
		result.MatchedTerms = descriptor.Keywords
		result.Descriptor = descriptor
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if trimmedQuery == "" || len(results) == 0 {
		return results, nil
	}
	documents := make([]bm25.Document, len(results))
	byID := make(map[string]mediadomain.MemeSearchResult, len(results))
	for i, result := range results {
		documents[i] = bm25.Document{ID: result.MemeID, Text: memeDescriptorText(result.Descriptor)}
		byID[result.MemeID] = result
	}
	ranked := bm25.Rank(trimmedQuery, documents, limit)
	results = results[:0]
	for _, item := range ranked {
		result := byID[item.ID]
		result.Score = item.Score
		results = append(results, result)
	}
	return results, nil
}

func memeDescriptorText(descriptor mediadomain.MemeDescriptor) string {
	return strings.Join([]string{descriptor.Title, descriptor.Summary, strings.Join(descriptor.Keywords, " "), strings.Join(descriptor.EmotionTags, " "), strings.Join(descriptor.SceneTags, " "), strings.Join(descriptor.UsageHints, " ")}, "\n")
}

func (s *Store) GetMeme(ctx context.Context, memeID string) (mediadomain.MemeAsset, mediadomain.MemeDescriptor, error) {
	var (
		asset        mediadomain.MemeAsset
		descriptor   mediadomain.MemeDescriptor
		keywordsJSON []byte
		emotionJSON  []byte
		sceneJSON    []byte
		usageJSON    []byte
		lastSentAt   sql.NullTime
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT a.meme_id, a.group_id, a.source_event_id, a.object_key, a.file_ext, a.content_hash,
		       a.perceptual_hash, a.width, a.height, a.animated, a.status, a.revision, a.created_at, a.last_sent_at,
		       a.send_count, a.dud_count,
		       d.title, d.summary, d.keywords_json, d.emotion_tags_json, d.scene_tags_json,
		       d.usage_hints_json, d.language, d.confidence, d.reviewed, d.updated_at
		FROM meme_assets a
		JOIN meme_descriptors d ON d.meme_id = a.meme_id
		WHERE a.meme_id = $1
	`, memeID).Scan(
		&asset.MemeID, &asset.GroupID, &asset.SourceEventID, &asset.ObjectKey, &asset.FileExt, &asset.ContentHash,
		&asset.PerceptualHash, &asset.Width, &asset.Height, &asset.Animated, &asset.Status, &asset.Revision, &asset.CreatedAt, &lastSentAt,
		&asset.SendCount, &asset.DudCount,
		&descriptor.Title, &descriptor.Summary, &keywordsJSON, &emotionJSON, &sceneJSON,
		&usageJSON, &descriptor.Language, &descriptor.Confidence, &descriptor.Reviewed, &descriptor.UpdatedAt,
	)
	if err != nil {
		return mediadomain.MemeAsset{}, mediadomain.MemeDescriptor{}, err
	}

	descriptor.MemeID = memeID
	_ = json.Unmarshal(keywordsJSON, &descriptor.Keywords)
	_ = json.Unmarshal(emotionJSON, &descriptor.EmotionTags)
	_ = json.Unmarshal(sceneJSON, &descriptor.SceneTags)
	_ = json.Unmarshal(usageJSON, &descriptor.UsageHints)
	if lastSentAt.Valid {
		asset.LastSentAt = &lastSentAt.Time
	}
	return asset, descriptor, nil
}

func (s *Store) CountMemesByGroup(ctx context.Context, groupID int64) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM meme_assets WHERE group_id = $1`,
		groupID,
	).Scan(&count)
	return count, err
}

func (s *Store) DeleteOldestMemes(ctx context.Context, groupID int64, deleteCount int) error {
	if deleteCount <= 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT meme_id FROM meme_assets WHERE group_id = $1 ORDER BY created_at ASC LIMIT $2`,
		groupID, deleteCount,
	)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			_ = rows.Close()
			return scanErr
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, len(ids))
	phs := make([]string, len(ids))
	for i, id := range ids {
		args[i] = id
		phs[i] = "$" + strconv.Itoa(i+1)
	}
	ph := strings.Join(phs, ",")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx,
		`DELETE FROM meme_descriptors WHERE meme_id IN (`+ph+`)`, args...); err != nil {
		return err
	}
	// 同库顺带清向量,避免孤儿向量仍被检索返回(旧 Qdrant 时代无法同事务做到)。
	if _, err = tx.ExecContext(ctx,
		`DELETE FROM meme_vectors WHERE meme_id IN (`+ph+`)`, args...); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx,
		`DELETE FROM meme_assets WHERE meme_id IN (`+ph+`)`, args...); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) MarkMemeSent(ctx context.Context, memeID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE meme_assets
		SET send_count = send_count + 1, last_sent_at = $1
		WHERE meme_id = $2
	`, time.Now(), memeID)
	return err
}

// MarkMemeDud 记一次哑弹（发送后群里持续冷场）。
func (s *Store) MarkMemeDud(ctx context.Context, memeID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE meme_assets
		SET dud_count = dud_count + 1
		WHERE meme_id = $1
	`, memeID)
	return err
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableTime(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return *value
}

func coalesceTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now()
	}
	return value
}
