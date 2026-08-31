package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
)

var (
	_ ports.MemoryStore        = (*Store)(nil)
	_ ports.LearningStateStore = (*Store)(nil)
	_ ports.ThoughtStore       = (*Store)(nil)
	_ ports.MemeStore          = (*Store)(nil)
	_ ports.ProfileStore       = (*Store)(nil)
	_ ports.OutboxStore        = (*Store)(nil)
)

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping mysql: %w", err)
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
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, NULL, ?, ?)
		ON DUPLICATE KEY UPDATE task_id = task_id
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
		WHERE (status IN (?, ?) AND available_at <= ?)
		   OR (status = ? AND (locked_until IS NULL OR locked_until <= ?))
		ORDER BY created_at ASC, task_id ASC
		LIMIT ?
		FOR UPDATE SKIP LOCKED
	`, ports.OutboxPending, ports.OutboxRetry, now, ports.OutboxRunning, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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
		task.Status = ports.OutboxRunning
		task.Attempts++
		task.LockedBy = workerID
		task.LockedUntil = now.Add(lease)
		task.UpdatedAt = now
		if _, err := tx.ExecContext(ctx, `UPDATE async_outbox SET status = ?, attempts = ?, locked_until = ?, locked_by = ?, updated_at = ? WHERE task_id = ?`,
			ports.OutboxRunning, task.Attempts, task.LockedUntil, workerID, now, task.ID); err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *Store) CompleteOutbox(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE async_outbox SET status = ?, locked_until = NULL, locked_by = NULL, updated_at = ? WHERE task_id = ?`, ports.OutboxCompleted, time.Now(), id)
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
	if err := s.db.QueryRowContext(ctx, `SELECT attempts, max_attempts FROM async_outbox WHERE task_id = ?`, id).Scan(&attempts, &maxAttempts); err != nil {
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
	_, err := s.db.ExecContext(ctx, `UPDATE async_outbox SET status = ?, available_at = COALESCE(?, available_at), locked_until = NULL, locked_by = NULL, last_error = ?, updated_at = ? WHERE task_id = ?`,
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
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			text_content = VALUES(text_content),
			segments_json = VALUES(segments_json),
			attachments_json = VALUES(attachments_json),
			mentioned_bot = VALUES(mentioned_bot),
			named_bot = VALUES(named_bot),
			is_reply_to_bot = VALUES(is_reply_to_bot),
			occurred_at = VALUES(occurred_at)
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
		WHERE group_id = ?
		ORDER BY occurred_at DESC
		LIMIT ?
	`, groupID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []conversationdomain.ConversationEvent{}
	for rows.Next() {
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
			return nil, err
		}
		event.Kind = conversationdomain.EventKind(kind)
		event.ReplyToMessageID = replyTo.String
		event.TimestampUnix = occurredAt.Unix()
		if err := json.Unmarshal(segmentsJSON, &event.Segments); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(attachmentsJSON, &event.Attachments); err != nil {
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
		WHERE group_id = ? AND (occurred_at > ? OR (occurred_at = ? AND event_id > ?))
		ORDER BY occurred_at ASC, event_id ASC
		LIMIT ?
	`, groupID, after, after, afterEventID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []conversationdomain.ConversationEvent{}
	for rows.Next() {
		var (
			event           conversationdomain.ConversationEvent
			replyTo         sql.NullString
			segmentsJSON    []byte
			attachmentsJSON []byte
			occurredAt      time.Time
			kind            string
		)
		if err := rows.Scan(
			&event.EventID, &event.GroupID, &event.UserID, &event.MessageID, &replyTo, &kind, &event.Text,
			&segmentsJSON, &attachmentsJSON, &event.MentionedBot, &event.NamedBot, &event.IsReplyToBot, &occurredAt,
		); err != nil {
			return nil, err
		}
		event.Kind = conversationdomain.EventKind(kind)
		event.ReplyToMessageID = replyTo.String
		event.TimestampUnix = occurredAt.Unix()
		if err := json.Unmarshal(segmentsJSON, &event.Segments); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(attachmentsJSON, &event.Attachments); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
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
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			scope = VALUES(scope),
			type = VALUES(type),
			subject = VALUES(subject),
			content = VALUES(content),
			source_event_id = VALUES(source_event_id),
			descriptor_ref = VALUES(descriptor_ref),
			confidence = VALUES(confidence),
			importance = VALUES(importance),
			expires_at = VALUES(expires_at),
			updated_at = VALUES(updated_at)
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
		base += " AND scope = ?"
		args = append(args, query.Scope)
	}
	if len(query.Types) > 0 {
		base += " AND type IN (" + placeholders(len(query.Types)) + ")"
		for _, memoryType := range query.Types {
			args = append(args, memoryType)
		}
	}
	if trimmed := strings.TrimSpace(query.Query); trimmed != "" {
		base += " AND (subject LIKE ? OR content LIKE ?)"
		like := "%" + trimmed + "%"
		args = append(args, like, like)
	}
	// 遗忘：过期记忆不再召回；重要性按时间贴现（高重要性衰减更慢，
	// 半衰 = importance*30 天）——重要旧事压过无聊近事，无聊近事先淡出。
	base += " AND (expires_at IS NULL OR expires_at > NOW())"
	base += " ORDER BY importance / (1 + TIMESTAMPDIFF(DAY, created_at, NOW()) / (GREATEST(importance, 0.1) * 30.0)) DESC, created_at DESC"
	if query.TopK > 0 {
		base += " LIMIT ?"
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

	_, err = tx.ExecContext(ctx, `
		INSERT INTO meme_assets (
			meme_id, group_id, source_event_id, object_key, file_ext, content_hash, perceptual_hash,
			width, height, animated, status, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			group_id = VALUES(group_id),
			source_event_id = VALUES(source_event_id),
			object_key = VALUES(object_key),
			file_ext = VALUES(file_ext),
			content_hash = VALUES(content_hash),
			perceptual_hash = VALUES(perceptual_hash),
			width = VALUES(width),
			height = VALUES(height),
			animated = VALUES(animated),
			status = VALUES(status)
	`, asset.MemeID, asset.GroupID, asset.SourceEventID, asset.ObjectKey, asset.FileExt, asset.ContentHash, asset.PerceptualHash,
		asset.Width, asset.Height, asset.Animated, asset.Status, coalesceTime(asset.CreatedAt))
	if err != nil {
		return err
	}

	keywordsJSON, _ := json.Marshal(descriptor.Keywords)
	emotionJSON, _ := json.Marshal(descriptor.EmotionTags)
	sceneJSON, _ := json.Marshal(descriptor.SceneTags)
	usageJSON, _ := json.Marshal(descriptor.UsageHints)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO meme_descriptors (
			meme_id, title, summary, keywords_json, emotion_tags_json, scene_tags_json,
			usage_hints_json, language, confidence, reviewed, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			title = VALUES(title),
			summary = VALUES(summary),
			keywords_json = VALUES(keywords_json),
			emotion_tags_json = VALUES(emotion_tags_json),
			scene_tags_json = VALUES(scene_tags_json),
			usage_hints_json = VALUES(usage_hints_json),
			language = VALUES(language),
			confidence = VALUES(confidence),
			reviewed = VALUES(reviewed),
			updated_at = VALUES(updated_at)
	`, descriptor.MemeID, descriptor.Title, descriptor.Summary, keywordsJSON, emotionJSON, sceneJSON,
		usageJSON, descriptor.Language, descriptor.Confidence, descriptor.Reviewed, coalesceTime(descriptor.UpdatedAt))
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) SearchMemes(ctx context.Context, query ports.MemeQuery) ([]mediadomain.MemeSearchResult, error) {
	like := "%" + strings.TrimSpace(query.Query) + "%"
	if like == "%%" {
		like = "%"
	}
	limit := query.TopK
	if limit <= 0 {
		limit = 5
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT a.meme_id, d.title, d.summary, d.keywords_json, d.emotion_tags_json, d.scene_tags_json,
		       d.usage_hints_json, d.language, d.confidence, d.reviewed
		FROM meme_assets a
		JOIN meme_descriptors d ON d.meme_id = a.meme_id
		WHERE (a.group_id = ? OR a.group_id = 0)
		  AND (d.title LIKE ? OR d.summary LIKE ? OR d.keywords_json LIKE ?)
		ORDER BY a.send_count DESC, d.updated_at DESC
		LIMIT ?
	`, query.GroupID, like, like, like, limit)
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
	return results, rows.Err()
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
		       a.perceptual_hash, a.width, a.height, a.animated, a.status, a.created_at, a.last_sent_at,
		       d.title, d.summary, d.keywords_json, d.emotion_tags_json, d.scene_tags_json,
		       d.usage_hints_json, d.language, d.confidence, d.reviewed, d.updated_at
		FROM meme_assets a
		JOIN meme_descriptors d ON d.meme_id = a.meme_id
		WHERE a.meme_id = ?
	`, memeID).Scan(
		&asset.MemeID, &asset.GroupID, &asset.SourceEventID, &asset.ObjectKey, &asset.FileExt, &asset.ContentHash,
		&asset.PerceptualHash, &asset.Width, &asset.Height, &asset.Animated, &asset.Status, &asset.CreatedAt, &lastSentAt,
		&descriptor.Title, &descriptor.Summary, &keywordsJSON, &emotionJSON, &sceneJSON,
		&usageJSON, &descriptor.Language, &descriptor.Confidence, &descriptor.Reviewed, &descriptor.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return mediadomain.MemeAsset{}, mediadomain.MemeDescriptor{}, err
		}
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
		`SELECT COUNT(*) FROM meme_assets WHERE group_id = ?`,
		groupID,
	).Scan(&count)
	return count, err
}

func (s *Store) DeleteOldestMemes(ctx context.Context, groupID int64, deleteCount int) error {
	if deleteCount <= 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT meme_id FROM meme_assets WHERE group_id = ? ORDER BY created_at ASC LIMIT ?`,
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
	ph := placeholders(len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
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
	if _, err = tx.ExecContext(ctx,
		`DELETE FROM meme_assets WHERE meme_id IN (`+ph+`)`, args...); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) MarkMemeSent(ctx context.Context, memeID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE meme_assets
		SET send_count = send_count + 1, last_sent_at = ?
		WHERE meme_id = ?
	`, time.Now(), memeID)
	return err
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
		WHERE group_id = ? AND user_id = ?
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
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			nickname = VALUES(nickname),
			message_count = VALUES(message_count),
			last_spoke_at = VALUES(last_spoke_at),
			active_score = VALUES(active_score),
			tags_json = VALUES(tags_json),
			common_phrases_json = VALUES(common_phrases_json),
			interests_json = VALUES(interests_json),
			traits_json = VALUES(traits_json),
			updated_at = VALUES(updated_at)
	`, profile.Stats.GroupID, profile.Stats.UserID, profile.Stats.Nickname, profile.Stats.MessageCount,
		coalesceTime(profile.Stats.LastSpokeAt), profile.Stats.ActiveScore, tagsJSON, commonPhrasesJSON, interestsJSON, traitsJSON, time.Now())
	return err
}

func (s *Store) GetRelationship(ctx context.Context, personaID string, groupID, userID int64) (profiledomain.RelationshipState, error) {
	var state profiledomain.RelationshipState
	err := s.db.QueryRowContext(ctx, `
		SELECT familiarity, affinity, tease_tolerance, grudge_score, last_interact_at
		FROM relationships
		WHERE persona_id = ? AND group_id = ? AND user_id = ?
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
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			familiarity = VALUES(familiarity),
			affinity = VALUES(affinity),
			tease_tolerance = VALUES(tease_tolerance),
			grudge_score = VALUES(grudge_score),
			last_interact_at = VALUES(last_interact_at)
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

func placeholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}
