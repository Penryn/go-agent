package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pgvector/pgvector-go"
	"github.com/phlin/go-agent/internal/chat"
	"github.com/phlin/go-agent/internal/conversation"
	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/qq"
	"github.com/phlin/go-agent/schema"
)

type Store struct {
	DB    *sql.DB
	lease *sql.Conn
}

// memoryMaintenanceLock is shared by note writers and taken exclusively by the
// duplicate cleanup command, so normalization and active-identity indexing see
// a stable table.
const memoryMaintenanceLock int64 = -7842231567990182

const activeIdentityIndexSQL = `CREATE UNIQUE INDEX IF NOT EXISTS friend_notes_active_identity ON groupfriend.notes (group_id,subject,kind,key,(CASE WHEN kind='episode' THEN topic_id ELSE '' END)) WHERE state='active'`

func Open(ctx context.Context, dsn string, dimensions int) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(10 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{DB: db}
	if err := s.Initialize(ctx, dimensions); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Initialize(ctx context.Context, dimensions int) error {
	if dimensions < 1 || dimensions > 4000 {
		return errors.New("vector dimensions must be between 1 and 4000")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range strings.Split(strings.ReplaceAll(schema.SQL, "__DIM__", strconv.Itoa(dimensions)), ";") {
		if strings.TrimSpace(statement) != "" {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply groupfriend schema: %w", err)
			}
		}
	}
	// One-time baseline for pre-ledger data. confidence=1 was a hardcoded
	// default, not a model measurement, and use_count counted recall attempts.
	// Both are cleared once so they cannot masquerade as new-semantics data.
	var migrated string
	err = tx.QueryRowContext(ctx, `SELECT value FROM groupfriend.settings WHERE key='memory_rework_migration'`).Scan(&migrated)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, `UPDATE groupfriend.notes SET confidence=0.7 WHERE confidence=1`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE groupfriend.notes SET use_count=0,last_used_at=NULL WHERE usage_version=0`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO groupfriend.settings(key,value) VALUES('memory_rework_migration','1')`); err != nil {
			return err
		}
	case err != nil:
		return err
	}
	if err := ensureActiveIdentityIndex(ctx, tx); err != nil {
		return err
	}
	expected := fmt.Sprint(dimensions)
	_, err = tx.ExecContext(ctx, `INSERT INTO groupfriend.settings(key,value) VALUES('vector_dimensions',$1) ON CONFLICT DO NOTHING`, expected)
	if err != nil {
		return err
	}
	var actual string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM groupfriend.settings WHERE key='vector_dimensions'`).Scan(&actual); err != nil {
		return err
	}
	if actual != expected {
		return errors.New("database vector dimensions differ; use a new database or rebuild the vector projection explicitly")
	}
	return tx.Commit()
}
func ensureActiveIdentityIndex(ctx context.Context, tx *sql.Tx) error {
	var pending bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM groupfriend.notes WHERE key<>lower(btrim(key)) OR topic_id<>btrim(topic_id)) OR EXISTS(SELECT 1 FROM groupfriend.notes WHERE state='active' GROUP BY group_id,subject,kind,lower(btrim(key)),(CASE WHEN kind='episode' THEN btrim(topic_id) ELSE '' END) HAVING COUNT(*)>1)`).Scan(&pending)
	if err != nil {
		return err
	}
	if pending {
		// The cleanup command must normalize and archive before the index can be
		// created; until then the app keeps working without active uniqueness.
		return nil
	}
	_, err = tx.ExecContext(ctx, activeIdentityIndexSQL)
	return err
}

// OpenMaintenance connects without applying schema or migrations. Maintenance
// commands use it so a dry run cannot mutate the database.
func OpenMaintenance(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}

func (s *Store) AcquireSender(ctx context.Context) error {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	var ok bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(-7842231567990181)`).Scan(&ok); err != nil || !ok {
		conn.Close()
		if err != nil {
			return err
		}
		return errors.New("another group-friend instance owns sending for this database")
	}
	s.lease = conn
	_, err = s.DB.ExecContext(ctx, `UPDATE groupfriend.reply_attempts SET state=CASE WHEN state='sending' THEN 'unknown' ELSE 'abandoned' END,body=jsonb_set(body,'{state}',to_jsonb(CASE WHEN state='sending' THEN 'unknown'::text ELSE 'abandoned'::text END)),updated_at=NOW() WHERE state IN ('planning','sending')`)
	return err
}
func (s *Store) Close() error {
	if s.lease != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = s.lease.ExecContext(ctx, `SELECT pg_advisory_unlock(-7842231567990181)`)
		cancel()
		_ = s.lease.Close()
	}
	return s.DB.Close()
}
func (s *Store) Ingest(ctx context.Context, e *qq.Event) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	body, err := json.Marshal(e)
	if err != nil {
		return false, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO groupfriend.messages(event_id,group_id,user_id,message_id,outbound,recalled,event_at,body) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(event_id) DO NOTHING RETURNING seq`, e.ID, e.GroupID, e.UserID, e.MessageID, e.Outbound, e.Recalled, e.At, body).Scan(&e.Seq)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if e.Recalled {
		if _, err := tx.ExecContext(ctx, `UPDATE groupfriend.messages SET recalled=TRUE WHERE group_id=$1 AND message_id=$2 AND event_id<>$3`, e.GroupID, e.MessageID, e.ID); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO groupfriend.group_progress(group_id) VALUES($1) ON CONFLICT DO NOTHING`, e.GroupID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
func (s *Store) Recent(ctx context.Context, group int64, limit int) ([]qq.Event, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT seq,body,recalled FROM (SELECT seq,body,recalled FROM groupfriend.messages WHERE group_id=$1 ORDER BY seq DESC LIMIT $2) t ORDER BY seq`, group, max(1, limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}
func scanEvents(rows *sql.Rows) ([]qq.Event, error) {
	out := []qq.Event{}
	for rows.Next() {
		var e qq.Event
		var b []byte
		var seq int64
		var recalled bool
		if err := rows.Scan(&seq, &b, &recalled); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &e); err != nil {
			return nil, err
		}
		e.Seq = seq
		e.Recalled = recalled
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) IsBotReply(ctx context.Context, group int64, id string) (bool, error) {
	var yes bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM groupfriend.messages WHERE group_id=$1 AND message_id=$2 AND outbound AND NOT recalled)`, group, id).Scan(&yes)
	return yes, err
}
func (s *Store) BeginAttempt(ctx context.Context, a chat.Attempt) (bool, error) {
	b, err := json.Marshal(a)
	if err != nil {
		return false, err
	}
	result, err := s.DB.ExecContext(ctx, `INSERT INTO groupfriend.reply_attempts(id,group_id,input_seq,state,at,body) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, a.ID, a.GroupID, a.InputSeq, a.State, a.At, b)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
func (s *Store) UpdateAttempt(ctx context.Context, a chat.Attempt) error {
	b, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE groupfriend.reply_attempts SET state=$2,sticker_id=$3,body=$4,updated_at=NOW() WHERE id=$1 AND state<>'confirmed'`, a.ID, a.State, a.Reply.StickerID, b)
	return err
}
func (s *Store) FinishAttempt(ctx context.Context, a chat.Attempt, e qq.Event, noteIDs []string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	body, err := json.Marshal(a)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE groupfriend.reply_attempts SET state='confirmed',sticker_id=$2,body=$3,updated_at=NOW() WHERE id=$1 AND state='sending'`, a.ID, a.Reply.StickerID, body)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errors.New("send attempt is no longer pending")
	}
	eb, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO groupfriend.messages(event_id,group_id,user_id,message_id,outbound,event_at,body) VALUES($1,$2,$3,$4,TRUE,$5,$6)`, e.ID, e.GroupID, e.UserID, e.MessageID, e.At, eb)
	if err != nil {
		return err
	}
	// The ledger is the fact source; the notes projection only advances for
	// rows this attempt actually inserted. note_usage has no notes foreign key,
	// so the source rows are validated here instead of at the schema level.
	inserted := []string{}
	if len(noteIDs) > 0 {
		rows, err := tx.QueryContext(ctx, `INSERT INTO groupfriend.note_usage(attempt_id,note_id,group_id,confirmed_at) SELECT $1,n.id,n.group_id,$3 FROM groupfriend.notes n WHERE n.group_id=$2 AND n.id=ANY($4) ON CONFLICT DO NOTHING RETURNING note_id`, a.ID, a.GroupID, e.At, noteIDs)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			inserted = append(inserted, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	if len(inserted) > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE groupfriend.notes SET use_count=CASE WHEN usage_version=1 THEN use_count+1 ELSE 1 END,usage_version=1,last_used_at=$2 WHERE id=ANY($1)`, inserted, e.At); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) RepliesSince(ctx context.Context, group int64, since time.Time) (int, time.Time, error) {
	var count int
	var last sql.NullTime
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FILTER(WHERE updated_at>=$2),MAX(updated_at) FROM groupfriend.reply_attempts WHERE group_id=$1 AND state='confirmed'`, group, since).Scan(&count, &last)
	return count, last.Time, err
}

const noteColumns = `id,group_id,subject,kind,key,text,evidence,revision,source_seq,state,updated_at,expires_at,salience,confidence,last_used_at,use_count,usage_version,valid_from,valid_to,memory_revision,topic_id`

func scanNotes(rows *sql.Rows) ([]memory.Note, error) {
	out := []memory.Note{}
	for rows.Next() {
		var n memory.Note
		var evidence []byte
		if err := rows.Scan(&n.ID, &n.GroupID, &n.Subject, &n.Kind, &n.Key, &n.Text, &evidence, &n.Version, &n.SourceSeq, &n.State, &n.Updated, &n.Expires, &n.Salience, &n.Confidence, &n.LastUsedAt, &n.UseCount, &n.UsageVersion, &n.ValidFrom, &n.ValidTo, &n.MemoryRevision, &n.TopicID); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(evidence, &n.Evidence); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ActiveNotes is the hot recall window: only currently valid active notes can
// enter the index, so expired or inactive rows cannot consume index_limit.
func (s *Store) ActiveNotes(ctx context.Context, group int64, limit int) ([]memory.Note, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+noteColumns+` FROM groupfriend.notes WHERE group_id=$1 AND state='active' AND (expires_at IS NULL OR expires_at>NOW()) AND (valid_from IS NULL OR valid_from<=NOW()) AND (valid_to IS NULL OR valid_to>NOW()) ORDER BY (kind='preference') DESC,updated_at DESC,id LIMIT $2`, group, max(1, limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNotes(rows)
}

// AllNotes is the administrative audit view and intentionally includes every
// lifecycle state.
func (s *Store) AllNotes(ctx context.Context, group int64, limit int) ([]memory.Note, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+noteColumns+` FROM groupfriend.notes WHERE group_id=$1 ORDER BY (kind='preference') DESC,updated_at DESC,id LIMIT $2`, group, max(1, limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNotes(rows)
}
func (s *Store) NotesByID(ctx context.Context, group int64, ids []string) ([]memory.Note, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+noteColumns+` FROM groupfriend.notes WHERE group_id=$1 AND id=ANY($2)`, group, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNotes(rows)
}

// NotesByIdentity returns every lifecycle state for the candidate identities,
// including forgotten rows that must stay visible to the reconciler.
func (s *Store) NotesByIdentity(ctx context.Context, group int64, identities []memory.Identity) ([]memory.Note, error) {
	out := []memory.Note{}
	for _, identity := range identities {
		rows, err := s.DB.QueryContext(ctx, `SELECT `+noteColumns+` FROM groupfriend.notes WHERE group_id=$1 AND subject=$2 AND kind=$3 AND lower(btrim(key))=$4 AND (kind<>'episode' OR btrim(topic_id)=$5) ORDER BY updated_at DESC,id`, group, identity.Subject, identity.Kind, identity.Key, identity.TopicID)
		if err != nil {
			return nil, err
		}
		notes, err := scanNotes(rows)
		rows.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, notes...)
	}
	return out, nil
}

func (s *Store) VectorNotes(ctx context.Context, group int64, v []float32, version string, limit int, threshold float64) ([]memory.Note, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+noteColumns+` FROM groupfriend.notes WHERE group_id=$1 AND state='active' AND (expires_at IS NULL OR expires_at>NOW()) AND (valid_from IS NULL OR valid_from<=NOW()) AND (valid_to IS NULL OR valid_to>NOW()) AND indexed_revision=revision AND embedding_model=$2 AND embedding IS NOT NULL AND (embedding <=> $3)<$5 ORDER BY embedding <=> $3 LIMIT $4`, group, version, pgvector.NewHalfVector(v), limit, threshold)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNotes(rows)
}
func (s *Store) SaveNotes(ctx context.Context, group int64, notes []memory.Note, p *memory.Progress) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, memoryMaintenanceLock); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, group); err != nil {
		return err
	}
	for _, n := range notes {
		if n.GroupID != group {
			return errors.New("memory scope mismatch")
		}
		proof, err := json.Marshal(n.Evidence)
		if err != nil {
			return err
		}
		if n.State == "active" {
			var blocked bool
			err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM groupfriend.notes n, jsonb_array_elements(n.evidence) old, jsonb_array_elements($2::jsonb) incoming WHERE n.group_id=$1 AND n.state='forgotten' AND old->>'event_id'=incoming->>'event_id')`, group, proof).Scan(&blocked)
			if err != nil {
				return err
			}
			if blocked {
				return errors.New("forgotten source cannot be reintroduced")
			}
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO groupfriend.notes(id,group_id,subject,kind,key,text,evidence,revision,source_seq,state,updated_at,expires_at,salience,confidence,valid_from,valid_to,memory_revision,topic_id) VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,$9,$10,$11,$12,$13,$14,$15,1,$17) ON CONFLICT(id) DO UPDATE SET text=EXCLUDED.text,evidence=EXCLUDED.evidence,revision=groupfriend.notes.revision+1,source_seq=EXCLUDED.source_seq,state=EXCLUDED.state,updated_at=EXCLUDED.updated_at,expires_at=EXCLUDED.expires_at,salience=EXCLUDED.salience,confidence=EXCLUDED.confidence,valid_from=EXCLUDED.valid_from,valid_to=EXCLUDED.valid_to,topic_id=EXCLUDED.topic_id,memory_revision=groupfriend.notes.memory_revision+1,embedding=NULL,indexed_revision=0 WHERE groupfriend.notes.revision=$16 AND groupfriend.notes.group_id=$2 AND groupfriend.notes.subject=$3 AND groupfriend.notes.key=$5 AND groupfriend.notes.kind=$4`, n.ID, group, n.Subject, n.Kind, n.Key, n.Text, proof, n.SourceSeq, n.State, n.Updated, n.Expires, n.Salience, n.Confidence, n.ValidFrom, n.ValidTo, n.Version, n.TopicID)
		if err != nil {
			return err
		}
		affected, _ := result.RowsAffected()
		if affected != 1 {
			return errors.New("memory revision changed")
		}
	}
	if p != nil {
		if p.GroupID != group {
			return errors.New("learning scope mismatch")
		}
		result, err := tx.ExecContext(ctx, `UPDATE groupfriend.group_progress SET after_seq=$3,retry_at=NOW() WHERE group_id=$1 AND after_seq=$2`, group, p.After, p.Until)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return errors.New("learning cursor changed")
		}
	}
	return tx.Commit()
}

func (s *Store) MemoryRevision(ctx context.Context, group int64) (int64, error) {
	var revision sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(memory_revision),0) FROM groupfriend.notes WHERE group_id=$1`, group).Scan(&revision)
	return revision.Int64, err
}

func (s *Store) RecordLearningRun(ctx context.Context, run memory.LearningRun) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO groupfriend.learning_runs(group_id,after_seq,until_seq,started_at,duration_ms,extract_ms,reconcile_ms,extract_tokens,reconcile_tokens,candidate_count,change_count,status,error_class) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, run.GroupID, run.AfterSeq, run.UntilSeq, run.StartedAt, run.DurationMS, run.ExtractMS, run.ReconcileMS, run.ExtractTokens, run.ReconcileTokens, run.CandidateCount, run.ChangeCount, run.Status, run.ErrorClass)
	if err != nil {
		return err
	}
	// The status endpoint only reads 24 hours; keep a bounded history for
	// incident review without letting telemetry grow forever.
	_, err = s.DB.ExecContext(ctx, `DELETE FROM groupfriend.learning_runs WHERE started_at < NOW()-INTERVAL '30 days'`)
	return err
}

func (s *Store) RecordMemoryEvent(ctx context.Context, group int64, kind string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO groupfriend.memory_events(group_id,kind) VALUES($1,$2)`, group, kind)
	return err
}

func (s *Store) SaveTopics(ctx context.Context, group int64, topics []conversation.TopicView, events []conversation.TopicEventView) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, topic := range topics {
		if topic.ID == "" {
			continue
		}
		participants, err := json.Marshal(topic.Participants)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO groupfriend.conversation_topics(group_id,topic_id,status,participants,first_seq,last_seq,revision,last_active_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(group_id,topic_id) DO UPDATE SET status=EXCLUDED.status,participants=EXCLUDED.participants,first_seq=EXCLUDED.first_seq,last_seq=EXCLUDED.last_seq,revision=GREATEST(groupfriend.conversation_topics.revision,EXCLUDED.revision),last_active_at=GREATEST(groupfriend.conversation_topics.last_active_at,EXCLUDED.last_active_at)`, group, topic.ID, topic.Status, participants, topic.FirstSeq, topic.LastSeq, max64(topic.Revision, 1), topic.LastActiveAt)
		if err != nil {
			return err
		}
		if topic.Summary != "" {
			_, err = tx.ExecContext(ctx, `INSERT INTO groupfriend.topic_summaries(group_id,topic_id,revision,summary,evidence,source_seq,updated_at) VALUES($1,$2,$3,$4,'[]',$5,NOW()) ON CONFLICT(group_id,topic_id,revision) DO UPDATE SET summary=EXCLUDED.summary,source_seq=EXCLUDED.source_seq,updated_at=EXCLUDED.updated_at`, group, topic.ID, max64(topic.SummaryRevision, 1), topic.Summary, topic.LastSeq)
			if err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM groupfriend.conversation_topic_events WHERE group_id=$1`, group); err != nil {
		return err
	}
	for _, event := range events {
		if event.EventID == "" {
			continue
		}
		topicIDs := event.TopicIDs
		if len(topicIDs) == 0 && event.TopicID != "" {
			topicIDs = []string{event.TopicID}
		}
		for _, topicID := range topicIDs {
			if topicID == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO groupfriend.conversation_topic_events(group_id,topic_id,event_id,message_id,seq,assigned_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, group, topicID, event.EventID, event.MessageID, event.Seq, event.AssignedAt); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) LoadTopics(ctx context.Context, group int64) ([]conversation.TopicView, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT t.topic_id,t.status,t.participants,t.first_seq,t.last_seq,t.revision,t.last_active_at,COALESCE((SELECT summary FROM groupfriend.topic_summaries x WHERE x.group_id=t.group_id AND x.topic_id=t.topic_id ORDER BY revision DESC LIMIT 1),''),COALESCE((SELECT revision FROM groupfriend.topic_summaries x WHERE x.group_id=t.group_id AND x.topic_id=t.topic_id ORDER BY revision DESC LIMIT 1),0) FROM groupfriend.conversation_topics t WHERE t.group_id=$1 ORDER BY t.last_active_at DESC LIMIT 64`, group)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []conversation.TopicView{}
	for rows.Next() {
		var t conversation.TopicView
		var participants []byte
		if err := rows.Scan(&t.ID, &t.Status, &participants, &t.FirstSeq, &t.LastSeq, &t.Revision, &t.LastActiveAt, &t.Summary, &t.SummaryRevision); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(participants, &t.Participants); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) LoadTopicEvents(ctx context.Context, group int64, limit int) ([]conversation.TopicEventView, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT topic_id,event_id,message_id,seq,assigned_at FROM (SELECT topic_id,event_id,message_id,seq,assigned_at FROM groupfriend.conversation_topic_events WHERE group_id=$1 ORDER BY seq DESC LIMIT $2) t ORDER BY seq`, group, max(1, limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []conversation.TopicEventView{}
	byEvent := map[string]int{}
	for rows.Next() {
		var event conversation.TopicEventView
		if err := rows.Scan(&event.TopicID, &event.EventID, &event.MessageID, &event.Seq, &event.AssignedAt); err != nil {
			return nil, err
		}
		if index, ok := byEvent[event.EventID]; ok {
			out[index].TopicIDs = append(out[index].TopicIDs, event.TopicID)
			continue
		}
		byEvent[event.EventID] = len(out)
		event.TopicIDs = []string{event.TopicID}
		out = append(out, event)
	}
	return out, rows.Err()
}

func (s *Store) SummaryBatch(ctx context.Context, limit int) (conversation.SummaryBatch, error) {
	var batch conversation.SummaryBatch
	err := s.DB.QueryRowContext(ctx, `SELECT p.group_id,p.summary_after_seq FROM groupfriend.group_progress p WHERE p.summary_retry_at<=NOW() AND EXISTS(SELECT 1 FROM groupfriend.conversation_topic_events e JOIN groupfriend.messages m ON m.event_id=e.event_id WHERE e.group_id=p.group_id AND m.seq>p.summary_after_seq) ORDER BY p.summary_retry_at,p.group_id LIMIT 1`).Scan(&batch.GroupID, &batch.AfterSeq)
	if errors.Is(err, sql.ErrNoRows) {
		return batch, nil
	}
	if err != nil {
		return batch, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT topic_id,revision,seq,body FROM (SELECT e.topic_id,COALESCE(t.revision,1) AS revision,m.seq,m.body,DENSE_RANK() OVER (ORDER BY m.seq) AS seq_rank FROM groupfriend.conversation_topic_events e JOIN groupfriend.messages m ON m.event_id=e.event_id LEFT JOIN groupfriend.conversation_topics t ON t.group_id=e.group_id AND t.topic_id=e.topic_id WHERE e.group_id=$1 AND m.seq>$2) ranked WHERE seq_rank<=$3 ORDER BY seq,topic_id`, batch.GroupID, batch.AfterSeq, max(1, limit))
	if err != nil {
		return batch, err
	}
	defer rows.Close()
	for rows.Next() {
		var event conversation.SummaryEvent
		var body []byte
		if err := rows.Scan(&event.TopicID, &event.Revision, &event.Seq, &body); err != nil {
			return batch, err
		}
		var message qq.Event
		if err := json.Unmarshal(body, &message); err != nil {
			return batch, err
		}
		event.EventID = message.ID
		event.Text = message.Text
		event.Outbound = message.Outbound
		batch.Events = append(batch.Events, event)
	}
	return batch, rows.Err()
}

func (s *Store) SaveTopicSummaries(ctx context.Context, group int64, summaries []conversation.TopicSummary, throughSeq int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, summary := range summaries {
		if summary.GroupID != group || summary.TopicID == "" || summary.Summary == "" {
			return errors.New("invalid topic summary")
		}
		evidence, err := json.Marshal(summary.EvidenceIDs)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO groupfriend.topic_summaries(group_id,topic_id,revision,summary,evidence,source_seq,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(group_id,topic_id,revision) DO UPDATE SET summary=EXCLUDED.summary,evidence=EXCLUDED.evidence,source_seq=GREATEST(groupfriend.topic_summaries.source_seq,EXCLUDED.source_seq),updated_at=EXCLUDED.updated_at`, group, summary.TopicID, max64(summary.Revision, 1), summary.Summary, evidence, summary.SourceSeq, summary.UpdatedAt)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO groupfriend.group_progress(group_id,summary_after_seq) VALUES($1,$2) ON CONFLICT(group_id) DO UPDATE SET summary_after_seq=GREATEST(groupfriend.group_progress.summary_after_seq,EXCLUDED.summary_after_seq),summary_retry_at=NOW()`, group, throughSeq)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeferTopicSummary(ctx context.Context, group int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE groupfriend.group_progress SET summary_retry_at=NOW()+INTERVAL '1 minute' WHERE group_id=$1`, group)
	return err
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
func (s *Store) LearningBatch(ctx context.Context, limit int) ([]qq.Event, memory.Progress, error) {
	var p memory.Progress
	err := s.DB.QueryRowContext(ctx, `SELECT p.group_id,p.after_seq FROM groupfriend.group_progress p WHERE retry_at<=NOW() AND EXISTS(SELECT 1 FROM groupfriend.messages m WHERE m.group_id=p.group_id AND m.seq>p.after_seq) ORDER BY retry_at,p.group_id LIMIT 1`).Scan(&p.GroupID, &p.After)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, p, nil
	}
	if err != nil {
		return nil, p, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT seq,body,recalled FROM groupfriend.messages WHERE group_id=$1 AND seq>$2 ORDER BY seq LIMIT $3`, p.GroupID, p.After, limit)
	if err != nil {
		return nil, p, err
	}
	defer rows.Close()
	events, err := scanEvents(rows)
	if len(events) > 0 {
		p.Until = events[len(events)-1].Seq
	}
	return events, p, err
}
func (s *Store) DeferLearning(ctx context.Context, group int64, delay time.Duration) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE groupfriend.group_progress SET retry_at=NOW()+make_interval(secs=>$2) WHERE group_id=$1`, group, delay.Seconds())
	return err
}
func (s *Store) PendingNotes(ctx context.Context, version string, limit int) ([]memory.Note, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+noteColumns+` FROM groupfriend.notes WHERE state='active' AND (expires_at IS NULL OR expires_at>NOW()) AND (valid_from IS NULL OR valid_from<=NOW()) AND (valid_to IS NULL OR valid_to>NOW()) AND (embedding IS NULL OR indexed_revision<>revision OR embedding_model<>$1) ORDER BY updated_at LIMIT $2`, version, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNotes(rows)
}
func (s *Store) IndexNote(ctx context.Context, n memory.Note, v []float32, version string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE groupfriend.notes SET embedding=$3,indexed_revision=$2,embedding_model=$4 WHERE id=$1 AND revision=$2 AND state='active' AND (expires_at IS NULL OR expires_at>NOW())`, n.ID, n.Version, pgvector.NewHalfVector(v), version)
	return err
}
