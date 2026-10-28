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
func (s *Store) FinishAttempt(ctx context.Context, a chat.Attempt, e qq.Event) error {
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
	return tx.Commit()
}
func (s *Store) RepliesSince(ctx context.Context, group int64, since time.Time) (int, time.Time, error) {
	var count int
	var last sql.NullTime
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FILTER(WHERE updated_at>=$2),MAX(updated_at) FROM groupfriend.reply_attempts WHERE group_id=$1 AND state='confirmed'`, group, since).Scan(&count, &last)
	return count, last.Time, err
}

const noteColumns = `id,group_id,subject,kind,key,text,evidence,revision,source_seq,state,updated_at,expires_at,salience,confidence,last_used_at,use_count,valid_from,valid_to,memory_revision`

func scanNotes(rows *sql.Rows) ([]memory.Note, error) {
	out := []memory.Note{}
	for rows.Next() {
		var n memory.Note
		var evidence []byte
		if err := rows.Scan(&n.ID, &n.GroupID, &n.Subject, &n.Kind, &n.Key, &n.Text, &evidence, &n.Version, &n.SourceSeq, &n.State, &n.Updated, &n.Expires, &n.Salience, &n.Confidence, &n.LastUsedAt, &n.UseCount, &n.ValidFrom, &n.ValidTo, &n.MemoryRevision); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(evidence, &n.Evidence); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func (s *Store) Notes(ctx context.Context, group int64, limit int) ([]memory.Note, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+noteColumns+` FROM groupfriend.notes WHERE group_id=$1 ORDER BY (kind='preference') DESC,updated_at DESC,id LIMIT $2`, group, max(1, limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNotes(rows)
}
func (s *Store) VectorNotes(ctx context.Context, group int64, v []float32, version string, limit int) ([]memory.Note, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+noteColumns+` FROM groupfriend.notes WHERE group_id=$1 AND state='active' AND (expires_at IS NULL OR expires_at>NOW()) AND indexed_revision=revision AND embedding_model=$2 AND embedding IS NOT NULL AND (embedding <=> $3)<0.6 ORDER BY embedding <=> $3 LIMIT $4`, group, version, pgvector.NewHalfVector(v), limit)
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
			err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM groupfriend.notes n, jsonb_array_elements(n.evidence) old, jsonb_array_elements($2::jsonb) incoming WHERE n.group_id=$1 AND n.state='forgotten' AND old->>'event_id'=incoming->>'event_id' AND (NOT $3 OR n.source_seq >= $4))`, group, proof, n.Explicit, n.SourceSeq).Scan(&blocked)
			if err != nil {
				return err
			}
			if blocked {
				return errors.New("forgotten source cannot be reintroduced")
			}
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO groupfriend.notes(id,group_id,subject,kind,key,text,evidence,revision,source_seq,state,updated_at,expires_at,salience,confidence,valid_from,valid_to,memory_revision) VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,$9,$10,$11,$12,$13,$14,$15,1) ON CONFLICT(id) DO UPDATE SET text=EXCLUDED.text,evidence=EXCLUDED.evidence,revision=groupfriend.notes.revision+1,source_seq=EXCLUDED.source_seq,state=EXCLUDED.state,updated_at=EXCLUDED.updated_at,expires_at=EXCLUDED.expires_at,salience=EXCLUDED.salience,confidence=EXCLUDED.confidence,valid_from=EXCLUDED.valid_from,valid_to=EXCLUDED.valid_to,memory_revision=groupfriend.notes.memory_revision+1,embedding=NULL,indexed_revision=0 WHERE groupfriend.notes.revision=$16 AND groupfriend.notes.group_id=$2 AND groupfriend.notes.subject=$3 AND groupfriend.notes.key=$5 AND groupfriend.notes.kind=$4`, n.ID, group, n.Subject, n.Kind, n.Key, n.Text, proof, n.SourceSeq, n.State, n.Updated, n.Expires, n.Salience, n.Confidence, n.ValidFrom, n.ValidTo, n.Version)
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
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(memory_revision),0) FROM groupfriend.notes WHERE group_id=$1`, group).Scan(&revision)
	return revision.Int64, err
}

func (s *Store) FlushNoteUsage(ctx context.Context, usage map[string]int64, at time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for id, count := range usage {
		if count < 1 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE groupfriend.notes SET use_count=use_count+$2,last_used_at=$3 WHERE id=$1 AND state='active'`, id, count, at); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SaveTopics(ctx context.Context, group int64, topics []conversation.TopicView, summaryAfterSeq int64) error {
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
		_, err = tx.ExecContext(ctx, `INSERT INTO groupfriend.conversation_topics(group_id,topic_id,status,participants,first_seq,last_seq,revision,last_active_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(group_id,topic_id) DO UPDATE SET status=EXCLUDED.status,participants=EXCLUDED.participants,first_seq=EXCLUDED.first_seq,last_seq=EXCLUDED.last_seq,revision=EXCLUDED.revision,last_active_at=EXCLUDED.last_active_at`, group, topic.ID, topic.Status, participants, topic.FirstSeq, topic.LastSeq, max64(topic.Revision, 1), topic.LastActiveAt)
		if err != nil {
			return err
		}
		if topic.Summary != "" {
			_, err = tx.ExecContext(ctx, `INSERT INTO groupfriend.topic_summaries(group_id,topic_id,revision,summary,evidence,source_seq,updated_at) VALUES($1,$2,$3,$4,'[]',$5,NOW()) ON CONFLICT(group_id,topic_id,revision) DO UPDATE SET summary=EXCLUDED.summary,source_seq=EXCLUDED.source_seq,updated_at=EXCLUDED.updated_at`, group, topic.ID, max64(topic.Revision, 1), topic.Summary, topic.LastSeq)
			if err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO groupfriend.group_progress(group_id,summary_after_seq) VALUES($1,$2) ON CONFLICT(group_id) DO UPDATE SET summary_after_seq=GREATEST(groupfriend.group_progress.summary_after_seq,EXCLUDED.summary_after_seq)`, group, summaryAfterSeq)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) LoadTopics(ctx context.Context, group int64) ([]conversation.TopicView, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT t.topic_id,t.status,t.participants,t.first_seq,t.last_seq,t.revision,t.last_active_at,COALESCE((SELECT summary FROM groupfriend.topic_summaries x WHERE x.group_id=t.group_id AND x.topic_id=t.topic_id ORDER BY revision DESC LIMIT 1),'') FROM groupfriend.conversation_topics t WHERE t.group_id=$1 ORDER BY t.last_active_at DESC LIMIT 64`, group)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []conversation.TopicView{}
	for rows.Next() {
		var t conversation.TopicView
		var participants []byte
		if err := rows.Scan(&t.ID, &t.Status, &participants, &t.FirstSeq, &t.LastSeq, &t.Revision, &t.LastActiveAt, &t.Summary); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(participants, &t.Participants); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) SummaryProgress(ctx context.Context, group int64) (int64, error) {
	var seq int64
	err := s.DB.QueryRowContext(ctx, `SELECT summary_after_seq FROM groupfriend.group_progress WHERE group_id=$1`, group).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return seq, err
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
func (s *Store) DeferLearning(ctx context.Context, group int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE groupfriend.group_progress SET retry_at=NOW()+INTERVAL '1 minute' WHERE group_id=$1`, group)
	return err
}
func (s *Store) PendingNotes(ctx context.Context, version string, limit int) ([]memory.Note, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+noteColumns+` FROM groupfriend.notes WHERE state='active' AND (expires_at IS NULL OR expires_at>NOW()) AND (embedding IS NULL OR indexed_revision<>revision OR embedding_model<>$1) ORDER BY updated_at LIMIT $2`, version, limit)
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
