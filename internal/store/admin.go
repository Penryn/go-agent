package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/phlin/go-agent/internal/chat"
	"github.com/phlin/go-agent/internal/meme"
)

type LearningLag struct {
	GroupID int64 `json:"group_id"`
	Lag     int64 `json:"lag"`
}

type LearningStats struct {
	Runs            int64   `json:"runs"`
	Succeeded       int64   `json:"succeeded"`
	Failed          int64   `json:"failed"`
	AvgDurationMS   float64 `json:"avg_duration_ms"`
	AvgExtractMS    float64 `json:"avg_extract_ms"`
	AvgReconcileMS  float64 `json:"avg_reconcile_ms"`
	ExtractTokens   int64   `json:"extract_tokens"`
	ReconcileTokens int64   `json:"reconcile_tokens"`
	ChangeCount     int64   `json:"change_count"`
}

type RecallStats struct {
	Attempted        int64   `json:"attempted"`
	Hits             int64   `json:"hits"`
	Empty            int64   `json:"empty"`
	CacheHits        int64   `json:"cache_hits"`
	CacheMisses      int64   `json:"cache_misses"`
	SemanticDegraded int64   `json:"semantic_degraded"`
	HitRate          float64 `json:"hit_rate"`
	EmptyRate        float64 `json:"empty_rate"`
	CacheHitRate     float64 `json:"cache_hit_rate"`
}

type LedgerStats struct {
	ConfirmedAttempts int64 `json:"confirmed_attempts"`
	UsageRows         int64 `json:"usage_rows"`
	WriteFailures     int64 `json:"write_failures"`
	// WriteRetries stays zero: a failed local archive is marked unknown and is
	// never retried automatically.
	WriteRetries int64 `json:"write_retries"`
}

type NoteStats struct {
	DuplicateIdentities  int64   `json:"duplicate_identities"`
	TotalIdentities      int64   `json:"total_identities"`
	DuplicateRate        float64 `json:"duplicate_rate"`
	ForgottenRebuilds    int64   `json:"forgotten_rebuilds"`
	ForgottenRebuildRate float64 `json:"forgotten_rebuild_rate"`
}

const memoryStatusWindowHours = 24

// MemoryStatus is the operational view of the learning/usage pipeline. Rolling
// counters cover the last 24 hours; ledger and note projections are lifetime
// values. ForgottenRebuildRate uses learning changes plus blocked rebuilds as
// its write baseline.
type MemoryStatus struct {
	WindowHours         int           `json:"window_hours"`
	Groups              []LearningLag `json:"groups"`
	Learning            LearningStats `json:"learning"`
	Recall              RecallStats   `json:"recall"`
	Ledger              LedgerStats   `json:"ledger"`
	Notes               NoteStats     `json:"notes"`
	PendingEmbeddings   int64         `json:"pending_embeddings"`
	UsageProjectionDiff int64         `json:"usage_projection_diff"`
}

func rate(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func (s *Store) MemoryStatus(ctx context.Context) (MemoryStatus, error) {
	status := MemoryStatus{WindowHours: memoryStatusWindowHours, Groups: []LearningLag{}}
	rows, err := s.DB.QueryContext(ctx, `SELECT p.group_id,GREATEST(COALESCE((SELECT MAX(m.seq) FROM groupfriend.messages m WHERE m.group_id=p.group_id),0)-p.after_seq,0) FROM groupfriend.group_progress p ORDER BY p.group_id`)
	if err != nil {
		return status, err
	}
	defer rows.Close()
	for rows.Next() {
		var lag LearningLag
		if err := rows.Scan(&lag.GroupID, &lag.Lag); err != nil {
			return status, err
		}
		status.Groups = append(status.Groups, lag)
	}
	if err := rows.Err(); err != nil {
		return status, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*),COUNT(*) FILTER (WHERE status='succeeded'),COUNT(*) FILTER (WHERE status='failed'),COALESCE(AVG(duration_ms)::float8,0),COALESCE(AVG(extract_ms)::float8,0),COALESCE(AVG(reconcile_ms)::float8,0),COALESCE(SUM(extract_tokens),0)::bigint,COALESCE(SUM(reconcile_tokens),0)::bigint,COALESCE(SUM(change_count),0)::bigint FROM groupfriend.learning_runs WHERE started_at>=NOW()-make_interval(hours=>$1)`, status.WindowHours).Scan(&status.Learning.Runs, &status.Learning.Succeeded, &status.Learning.Failed, &status.Learning.AvgDurationMS, &status.Learning.AvgExtractMS, &status.Learning.AvgReconcileMS, &status.Learning.ExtractTokens, &status.Learning.ReconcileTokens, &status.Learning.ChangeCount); err != nil {
		return status, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FILTER (WHERE body->'retrieval'->>'recall_attempted'='true'),COUNT(*) FILTER (WHERE body->'retrieval'->>'recall_attempted'='true' AND COALESCE((body->'retrieval'->>'recall_hits')::int,0)>0),COUNT(*) FILTER (WHERE body->'retrieval'->>'recall_attempted'='true' AND COALESCE((body->'retrieval'->>'recall_hits')::int,0)=0),COUNT(*) FILTER (WHERE body->'retrieval'->>'recall_attempted'='true' AND body->'retrieval'->>'recall_cache_hit'='true'),COUNT(*) FILTER (WHERE body->'retrieval'->>'recall_attempted'='true' AND COALESCE(body->'retrieval'->>'recall_cache_hit','false')<>'true'),COUNT(*) FILTER (WHERE body->'retrieval'->'degraded' @> '["semantic"]'::jsonb) FROM groupfriend.reply_attempts WHERE updated_at>=NOW()-make_interval(hours=>$1)`, status.WindowHours).Scan(&status.Recall.Attempted, &status.Recall.Hits, &status.Recall.Empty, &status.Recall.CacheHits, &status.Recall.CacheMisses, &status.Recall.SemanticDegraded); err != nil {
		return status, err
	}
	status.Recall.HitRate = rate(status.Recall.Hits, status.Recall.Attempted)
	status.Recall.EmptyRate = rate(status.Recall.Empty, status.Recall.Attempted)
	status.Recall.CacheHitRate = rate(status.Recall.CacheHits, status.Recall.Attempted)
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM groupfriend.reply_attempts WHERE state='confirmed'`).Scan(&status.Ledger.ConfirmedAttempts); err != nil {
		return status, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM groupfriend.note_usage`).Scan(&status.Ledger.UsageRows); err != nil {
		return status, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM groupfriend.reply_attempts WHERE state='unknown' AND body->>'error'='delivery confirmed but archive failed' AND updated_at>=NOW()-make_interval(hours=>$1)`, status.WindowHours).Scan(&status.Ledger.WriteFailures); err != nil {
		return status, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN c>1 THEN 1 ELSE 0 END),0)::bigint,COUNT(*) FROM (SELECT COUNT(*) AS c FROM groupfriend.notes GROUP BY group_id,subject,kind,key,(CASE WHEN kind='episode' THEN topic_id ELSE '' END)) identities`).Scan(&status.Notes.DuplicateIdentities, &status.Notes.TotalIdentities); err != nil {
		return status, err
	}
	status.Notes.DuplicateRate = rate(status.Notes.DuplicateIdentities, status.Notes.TotalIdentities)
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM groupfriend.memory_events WHERE kind='forgotten_blocked' AND at>=NOW()-make_interval(hours=>$1)`, status.WindowHours).Scan(&status.Notes.ForgottenRebuilds); err != nil {
		return status, err
	}
	status.Notes.ForgottenRebuildRate = rate(status.Notes.ForgottenRebuilds, status.Learning.ChangeCount+status.Notes.ForgottenRebuilds)
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM groupfriend.notes WHERE state='active' AND (embedding IS NULL OR indexed_revision<>revision)`).Scan(&status.PendingEmbeddings); err != nil {
		return status, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM groupfriend.notes n LEFT JOIN (SELECT note_id,COUNT(*) c FROM groupfriend.note_usage GROUP BY note_id) u ON u.note_id=n.id WHERE n.usage_version=1 AND n.use_count<>COALESCE(u.c,0)`).Scan(&status.UsageProjectionDiff); err != nil {
		return status, err
	}
	return status, nil
}

func (s *Store) CheckSender(ctx context.Context) error {
	if s.lease == nil {
		return errors.New("sender lease not acquired")
	}
	return s.lease.PingContext(ctx)
}
func (s *Store) Groups(ctx context.Context) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT group_id FROM groupfriend.group_progress ORDER BY group_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (s *Store) Attempts(ctx context.Context, group int64) ([]chat.Attempt, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT body,state FROM groupfriend.reply_attempts WHERE group_id=$1 ORDER BY at DESC LIMIT 50`, group)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []chat.Attempt{}
	for rows.Next() {
		var b []byte
		var a chat.Attempt
		var state string
		if err := rows.Scan(&b, &state); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &a); err != nil {
			return nil, err
		}
		a.State = state
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) EditNote(ctx context.Context, group int64, id string, version int64, text string, forget bool) error {
	if len([]rune(text)) > 600 || (!forget && text == "") {
		return errors.New("invalid note text")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, group); err != nil {
		return err
	}
	state := "active"
	if forget {
		state = "forgotten"
		text = ""
	}
	result, err := tx.ExecContext(ctx, `UPDATE groupfriend.notes SET text=$4,state=$5,revision=revision+1,embedding=NULL,indexed_revision=0,updated_at=NOW(),source_seq=GREATEST(source_seq,COALESCE((SELECT MAX(seq) FROM groupfriend.messages WHERE group_id=$1),0)) WHERE group_id=$1 AND id=$2 AND revision=$3`, group, id, version, text, state)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errors.New("note not found or revision changed")
	}
	return tx.Commit()
}
func (s *Store) AdminStickers(ctx context.Context) ([]meme.Asset, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+stickerColumns+` FROM groupfriend.stickers ORDER BY last_seen DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStickers(rows)
}
func (s *Store) EditSticker(ctx context.Context, id string, version int64, text string, blocked bool) error {
	if len([]rune(text)) > 400 || (!blocked && text == "") {
		return errors.New("invalid description")
	}
	state := "ready"
	if blocked {
		state = "blocked"
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE groupfriend.stickers SET description=$3,state=$4,revision=revision+1,embedding=NULL,indexed_revision=0 WHERE id=$1 AND revision=$2 AND (path<>'' OR $4='blocked')`, id, version, text, state)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errors.New("sticker missing, has no file, or revision changed")
	}
	return nil
}
