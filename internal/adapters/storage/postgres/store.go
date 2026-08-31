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

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
)

var (
	_ ports.MemoryStore  = (*Store)(nil)
	_ ports.OutboxStore  = (*Store)(nil)
	_ ports.ProfileStore = (*Store)(nil)
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
	now := time.Now()
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memories (
			memory_id, scope, type, subject, content, source_event_id, descriptor_ref,
			confidence, importance, created_at, expires_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (memory_id) DO UPDATE SET
			scope = EXCLUDED.scope,
			type = EXCLUDED.type,
			subject = EXCLUDED.subject,
			content = EXCLUDED.content,
			source_event_id = EXCLUDED.source_event_id,
			descriptor_ref = EXCLUDED.descriptor_ref,
			confidence = EXCLUDED.confidence,
			importance = EXCLUDED.importance,
			expires_at = EXCLUDED.expires_at,
			updated_at = EXCLUDED.updated_at
	`, record.MemoryID, record.Scope, record.Type, record.Subject, record.Content, record.SourceEventID, record.DescriptorRef,
		record.Confidence, record.Importance, createdAt, nullableTime(record.ExpiresAt), now)
	return err
}

func (s *Store) QueryMemories(ctx context.Context, query ports.MemoryQuery) ([]memorydomain.MemoryRecord, error) {
	base := `
		SELECT memory_id, scope, type, subject, content, source_event_id, descriptor_ref,
		       confidence, importance, created_at, expires_at
		FROM memories
		WHERE 1=1
	`
	args := []any{}
	if query.Scope != "" {
		base += " AND scope = $" + strconv.Itoa(len(args)+1)
		args = append(args, query.Scope)
	}
	if len(query.Types) > 0 {
		phs := make([]string, 0, len(query.Types))
		for _, memoryType := range query.Types {
			args = append(args, memoryType)
			phs = append(phs, "$"+strconv.Itoa(len(args)))
		}
		base += " AND type IN (" + strings.Join(phs, ",") + ")"
	}
	if trimmed := strings.TrimSpace(query.Query); trimmed != "" {
		like := "%" + trimmed + "%"
		base += " AND (subject LIKE $" + strconv.Itoa(len(args)+1) + " OR content LIKE $" + strconv.Itoa(len(args)+2) + ")"
		args = append(args, like, like)
	}
	base += " ORDER BY importance DESC, created_at DESC"
	if query.TopK > 0 {
		base += " LIMIT $" + strconv.Itoa(len(args)+1)
		args = append(args, query.TopK)
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
	return records, rows.Err()
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
