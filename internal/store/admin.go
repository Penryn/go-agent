package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/phlin/go-agent/internal/chat"
	"github.com/phlin/go-agent/internal/meme"
)

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
