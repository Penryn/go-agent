package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/pgvector/pgvector-go"
	"github.com/phlin/go-agent/internal/meme"
)

const stickerColumns = `id,COALESCE(content_hash,''),source_event,source_group,url,path,mime,size,platform_sticker,description,tags,state,revision,last_seen`

func scanSticker(scan func(...any) error) (meme.Asset, error) {
	var a meme.Asset
	var tags []byte
	err := scan(&a.ID, &a.Hash, &a.SourceID, &a.SourceGroup, &a.URL, &a.Path, &a.MIME, &a.Size, &a.Sticker, &a.Description, &tags, &a.State, &a.Version, &a.LastSeen)
	if err == nil {
		err = json.Unmarshal(tags, &a.Tags)
	}
	return a, err
}
func scanStickers(rows *sql.Rows) ([]meme.Asset, error) {
	out := []meme.Asset{}
	for rows.Next() {
		a, err := scanSticker(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) StageSticker(ctx context.Context, a meme.Asset) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO groupfriend.stickers(id,source_event,source_group,url,platform_sticker,last_seen) SELECT $1,$2,$3,$4,$5,$6 WHERE (SELECT COUNT(*) FROM groupfriend.stickers WHERE state='pending')<256 ON CONFLICT(id) DO NOTHING`, a.ID, a.SourceID, a.SourceGroup, a.URL, a.Sticker, a.LastSeen)
	return err
}
func (s *Store) PendingStickers(ctx context.Context, limit int) ([]meme.Asset, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+stickerColumns+` FROM groupfriend.stickers WHERE state='pending' AND retry_at<=NOW() AND attempts<5 ORDER BY last_seen LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStickers(rows)
}
func (s *Store) CacheSticker(ctx context.Context, a meme.Asset) (meme.Asset, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return a, err
	}
	defer tx.Rollback()
	// Global content identity is independent of URL and source group.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, a.Hash); err != nil {
		return a, err
	}
	old, err := scanSticker(tx.QueryRowContext(ctx, `SELECT `+stickerColumns+` FROM groupfriend.stickers WHERE content_hash=$1`, a.Hash).Scan)
	if err == nil && old.ID != a.ID {
		if _, err := tx.ExecContext(ctx, `UPDATE groupfriend.stickers SET last_seen=$2 WHERE id=$1`, old.ID, time.Now()); err != nil {
			return a, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM groupfriend.stickers WHERE id=$1 AND state='pending'`, a.ID); err != nil {
			return a, err
		}
		if old.State == "evicted" {
			if _, err := tx.ExecContext(ctx, `UPDATE groupfriend.stickers SET state='pending',path=$2,mime=$3,size=$4,attempts=0,retry_at=NOW(),revision=revision+1 WHERE id=$1 AND state='evicted'`, old.ID, a.Path, a.MIME, a.Size); err != nil {
				return a, err
			}
			old.State = "pending"
			old.Path = a.Path
			old.MIME = a.MIME
			old.Size = a.Size
			old.Version++
		}
		return old, tx.Commit()
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return a, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE groupfriend.stickers SET content_hash=$2,path=$3,mime=$4,size=$5 WHERE id=$1 AND state='pending' AND revision=$6`, a.ID, a.Hash, a.Path, a.MIME, a.Size, a.Version)
	if err != nil {
		return a, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return a, errors.New("sticker state changed")
	}
	return a, tx.Commit()
}
func (s *Store) DescribeSticker(ctx context.Context, a meme.Asset) error {
	tags, err := json.Marshal(a.Tags)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE groupfriend.stickers SET state=$2,description=$3,tags=$4,revision=revision+1,embedding=NULL,indexed_revision=0,last_error='' WHERE id=$1 AND state='pending' AND revision=$5`, a.ID, a.State, a.Description, tags, a.Version)
	return err
}
func (s *Store) DeferSticker(ctx context.Context, id, reason string) error {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE groupfriend.stickers SET attempts=attempts+1,state=CASE WHEN attempts>=4 THEN 'rejected' ELSE 'pending' END,retry_at=NOW()+INTERVAL '1 minute',last_error=$2 WHERE id=$1 AND state='pending'`, id, reason)
	return err
}
func (s *Store) StickerCandidates(ctx context.Context, group int64, cooldown time.Duration, limit int) ([]meme.Asset, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+stickerColumns+` FROM groupfriend.stickers s WHERE state='ready' AND path<>'' AND NOT EXISTS(SELECT 1 FROM groupfriend.reply_attempts a WHERE a.group_id=$1 AND a.sticker_id=s.id AND a.state='confirmed' AND a.updated_at>$2) ORDER BY last_seen DESC,id LIMIT $3`, group, time.Now().Add(-cooldown), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStickers(rows)
}
func (s *Store) VectorStickers(ctx context.Context, v []float32, version string, limit int) ([]meme.Asset, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+stickerColumns+` FROM groupfriend.stickers WHERE state='ready' AND indexed_revision=revision AND embedding_model=$1 AND embedding IS NOT NULL AND (embedding <=> $2)<0.6 ORDER BY embedding <=> $2 LIMIT $3`, version, pgvector.NewHalfVector(v), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStickers(rows)
}
func (s *Store) GetSticker(ctx context.Context, id string) (meme.Asset, error) {
	return scanSticker(s.DB.QueryRowContext(ctx, `SELECT `+stickerColumns+` FROM groupfriend.stickers WHERE id=$1`, id).Scan)
}
func (s *Store) PendingStickerIndexes(ctx context.Context, version string, limit int) ([]meme.Asset, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+stickerColumns+` FROM groupfriend.stickers WHERE state='ready' AND (embedding IS NULL OR indexed_revision<>revision OR embedding_model<>$1) ORDER BY last_seen DESC LIMIT $2`, version, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStickers(rows)
}
func (s *Store) IndexSticker(ctx context.Context, a meme.Asset, v []float32, version string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE groupfriend.stickers SET embedding=$3,indexed_revision=$2,embedding_model=$4 WHERE id=$1 AND revision=$2 AND state='ready'`, a.ID, a.Version, pgvector.NewHalfVector(v), version)
	return err
}
func (s *Store) TrimStickers(ctx context.Context, maxItems int, maxBytes int64) ([]string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(-7842231567990182)`); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,path,size FROM groupfriend.stickers WHERE path<>'' ORDER BY (state='pending') DESC,(state='ready') DESC,last_seen DESC,id`)
	if err != nil {
		return nil, err
	}
	type file struct {
		id, path string
		size     int64
	}
	files := []file{}
	for rows.Next() {
		var f file
		if err := rows.Scan(&f.id, &f.path, &f.size); err != nil {
			rows.Close()
			return nil, err
		}
		files = append(files, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	paths := []string{}
	var total int64
	for i, f := range files {
		total += f.size
		if (maxItems > 0 && i >= maxItems) || (maxBytes > 0 && total > maxBytes) {
			if _, err := tx.ExecContext(ctx, `UPDATE groupfriend.stickers SET state=CASE WHEN state='blocked' THEN 'blocked' ELSE 'evicted' END,path='',size=0,embedding=NULL,indexed_revision=0,revision=revision+1 WHERE id=$1`, f.id); err != nil {
				return nil, err
			}
			paths = append(paths, f.path)
		}
	}
	return paths, tx.Commit()
}
func (s *Store) DisableSticker(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE groupfriend.stickers SET state='blocked',embedding=NULL,indexed_revision=0,revision=revision+1 WHERE id=$1`, id)
	return err
}

// StickerRevision changes whenever any asset is staged, described, disabled or
// evicted, so recall cache keys cannot survive a material state change.
func (s *Store) StickerRevision(ctx context.Context) (int64, error) {
	var revision sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(revision),0) FROM groupfriend.stickers`).Scan(&revision)
	return revision.Int64, err
}
