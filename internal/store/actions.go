package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/phlin/go-agent/internal/chat"
	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/qq"
)

// SaveDelivery persists the platform receipt before local archiving, so an
// archive failure still leaves the reconciler a verifiable message id.
func (s *Store) SaveDelivery(ctx context.Context, attemptID, messageID, kind string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE groupfriend.reply_attempts SET delivery_message_id=$2,delivery_kind=$3 WHERE id=$1`, attemptID, messageID, kind)
	return err
}

// SavePendingMemory stages the prepared note snapshot for one attempt. The
// row is written once; a re-staged attempt keeps its original snapshot.
func (s *Store) SavePendingMemory(ctx context.Context, attemptID string, group int64, notes []memory.NoteSnapshot) error {
	body, err := json.Marshal(notes)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO groupfriend.reply_memory_pending(attempt_id,group_id,changes,state) VALUES($1,$2,$3,'pending') ON CONFLICT(attempt_id) DO NOTHING`, attemptID, group, body)
	return err
}

// DiscardPendingMemory abandons staged memory for attempts that never reach a
// platform confirmation; the reason is kept for audit.
func (s *Store) DiscardPendingMemory(ctx context.Context, attemptID, reason string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE groupfriend.reply_memory_pending SET state='discarded',last_error=$2,updated_at=NOW() WHERE attempt_id=$1 AND state='pending'`, attemptID, reason)
	return err
}

// MarkReconcile opts an unknown action attempt into local reconciliation: the
// platform already confirmed the action and only the local archive failed. It
// also stamps the delivery kind, because the failed confirmation transaction
// rolled back before it could.
func (s *Store) MarkReconcile(ctx context.Context, attemptID, reason string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE groupfriend.reply_attempts SET delivery_kind='action',reconcile_state='pending',reconcile_error=$2,reconcile_at=NOW() WHERE id=$1 AND state='unknown'`, attemptID, reason)
	return err
}

// ReconcileCandidate is one unknown attempt due for local reconciliation.
// Message-kind candidates always carry the persisted platform receipt.
type ReconcileCandidate struct {
	Attempt   chat.Attempt
	Kind      string
	MessageID string
}

// ReconcileDue lists unknown attempts ready for local closure: message sends
// with a persisted receipt, and action attempts explicitly marked pending.
// Uncertain sends without a receipt stay unknown — there is no reliable query.
func (s *Store) ReconcileDue(ctx context.Context, limit int) ([]ReconcileCandidate, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT body,delivery_kind,delivery_message_id FROM groupfriend.reply_attempts WHERE state='unknown' AND ((delivery_kind='message' AND delivery_message_id<>'' AND reconcile_state IN ('none','pending')) OR (delivery_kind='action' AND reconcile_state='pending')) AND (reconcile_at IS NULL OR reconcile_at<=NOW()) ORDER BY updated_at LIMIT $1`, max(1, limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReconcileCandidate{}
	for rows.Next() {
		var body []byte
		var candidate ReconcileCandidate
		if err := rows.Scan(&body, &candidate.Kind, &candidate.MessageID); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(body, &candidate.Attempt); err != nil {
			return nil, fmt.Errorf("reconcile attempt %s: %w", candidate.Attempt.ID, err)
		}
		out = append(out, candidate)
	}
	return out, rows.Err()
}

// ReconcileOutbound closes a reconciled message attempt: the outbound event is
// archived idempotently, the attempt turns confirmed, usage is booked and
// staged memory commits — all in one transaction, with no platform call.
func (s *Store) ReconcileOutbound(ctx context.Context, attempt chat.Attempt, e qq.Event, noteIDs []string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE groupfriend.reply_attempts SET state='confirmed',reconcile_state='resolved',reconcile_error='',updated_at=NOW() WHERE id=$1 AND state='unknown'`, attempt.ID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return errors.New("reconcile attempt is no longer unknown")
	}
	eb, err := json.Marshal(e)
	if err != nil {
		return err
	}
	// ON CONFLICT DO NOTHING covers both the event_id uniqueness and the
	// outbound (group_id, message_id) partial index.
	if _, err := tx.ExecContext(ctx, `INSERT INTO groupfriend.messages(event_id,group_id,user_id,message_id,outbound,event_at,body) VALUES($1,$2,$3,$4,TRUE,$5,$6) ON CONFLICT DO NOTHING`, e.ID, e.GroupID, e.UserID, e.MessageID, e.At, eb); err != nil {
		return err
	}
	inserted := []string{}
	if len(noteIDs) > 0 {
		rows, err := tx.QueryContext(ctx, `INSERT INTO groupfriend.note_usage(attempt_id,note_id,group_id,confirmed_at) SELECT $1,n.id,n.group_id,$3 FROM groupfriend.notes n WHERE n.group_id=$2 AND n.id=ANY($4) ON CONFLICT DO NOTHING RETURNING note_id`, attempt.ID, attempt.GroupID, e.At, noteIDs)
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
	if err := s.commitPending(ctx, tx, attempt.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeferReconcile keeps an unknown attempt pending with a delayed retry. The
// platform could not be uniquely confirmed; text or time similarity is never
// used to guess the outcome. Message candidates surface with state 'none',
// so both entry states can be deferred.
func (s *Store) DeferReconcile(ctx context.Context, attemptID, reason string, next time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE groupfriend.reply_attempts SET reconcile_state='pending',reconcile_at=$3,reconcile_error=$2 WHERE id=$1 AND state='unknown' AND reconcile_state IN ('none','pending')`, attemptID, reason, next)
	return err
}

// FailReconcile marks an attempt unresolvable: the platform definitively
// contradicts the stored send (no such message, or a foreign identity).
func (s *Store) FailReconcile(ctx context.Context, attemptID, reason string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE groupfriend.reply_attempts SET reconcile_state='unresolvable',reconcile_error=$2 WHERE id=$1 AND state='unknown'`, attemptID, reason)
	return err
}

// ConfirmAction commits a confirmed platform action in one transaction: the
// attempt turns confirmed, the action fact and topic projection advance, the
// consulted notes are booked, and staged memory commits. It never writes an
// outbound message and never touches notes.use_count.
func (s *Store) ConfirmAction(ctx context.Context, a chat.Attempt, rec chat.ActionRecord, noteIDs []string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE groupfriend.reply_attempts SET state='confirmed',delivery_kind='action',updated_at=NOW() WHERE id=$1 AND state<>'confirmed'`, a.ID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return errors.New("action attempt is no longer pending")
	}
	if rec.TopicID != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO groupfriend.conversation_topic_actions(attempt_id,group_id,topic_id,action_kind,target_event_id,target_message_id,confirmed_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, a.ID, rec.GroupID, rec.TopicID, rec.Kind, rec.TargetEventID, rec.TargetMessageID, rec.ConfirmedAt); err != nil {
			return err
		}
		// The topic row may not be persisted yet; the tracker snapshot upsert
		// carries this timestamp on the next save.
		if _, err := tx.ExecContext(ctx, `UPDATE groupfriend.conversation_topics SET bot_action_at=GREATEST(COALESCE(bot_action_at,to_timestamp(0)),$3),last_active_at=GREATEST(last_active_at,$3) WHERE group_id=$1 AND topic_id=$2`, rec.GroupID, rec.TopicID, rec.ConfirmedAt); err != nil {
			return err
		}
	}
	if len(noteIDs) > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO groupfriend.note_consultation(attempt_id,note_id,group_id,action_kind,confirmed_at) SELECT $1,n.id,$2,$4,$3 FROM groupfriend.notes n WHERE n.group_id=$2 AND n.id=ANY($5) ON CONFLICT DO NOTHING`, a.ID, rec.GroupID, rec.ConfirmedAt, rec.Kind, noteIDs); err != nil {
			return err
		}
	}
	if err := s.commitPending(ctx, tx, a.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// commitPending applies staged memory inside the caller's transaction. The
// attempt_id row is the idempotency key: committed and discarded snapshots
// are left untouched, so retries never double-write notes.
func (s *Store) commitPending(ctx context.Context, tx *sql.Tx, attemptID string) error {
	var group int64
	var raw []byte
	var state string
	err := tx.QueryRowContext(ctx, `SELECT group_id,changes,state FROM groupfriend.reply_memory_pending WHERE attempt_id=$1 FOR UPDATE`, attemptID).Scan(&group, &raw, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if state != "pending" {
		return nil
	}
	snapshots := []memory.NoteSnapshot{}
	if err := json.Unmarshal(raw, &snapshots); err != nil {
		return fmt.Errorf("pending memory for %s: %w", attemptID, err)
	}
	if len(snapshots) > 0 {
		notes := make([]memory.Note, 0, len(snapshots))
		for _, snapshot := range snapshots {
			notes = append(notes, snapshot.Expanded())
		}
		if err := s.saveNotesTx(ctx, tx, group, notes, nil); err != nil {
			return fmt.Errorf("commit pending memory: %w", err)
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE groupfriend.reply_memory_pending SET state='committed',last_error='',updated_at=NOW() WHERE attempt_id=$1`, attemptID)
	return err
}
