package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pgvector/pgvector-go"
	"github.com/phlin/go-agent/internal/memory"
)

func scanPlan(t *testing.T, rows *sql.Rows) string {
	t.Helper()
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return plan.String()
}

// TestVectorRecallPlanUsesHNSWIndex seeds a representative note volume and runs
// EXPLAIN (ANALYZE, BUFFERS) on the parameterized threshold query. The default
// plan is logged because it depends on table statistics; with sequential scans
// disabled the HNSW index must remain usable for the query shape.
func TestVectorRecallPlanUsesHNSWIndex(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const total = 500
	seeded := make([]memory.Note, 0, total)
	for i := 0; i < total; i++ {
		seeded = append(seeded, memory.Note{
			ID: fmt.Sprintf("vec-%04d", i), GroupID: 1, Subject: int64(i%40 + 1), Kind: "episode",
			Key: fmt.Sprintf("topic-%d", i), Text: fmt.Sprintf("评测记忆 %d", i), State: "active", Updated: time.Now(),
			Evidence: []memory.Evidence{{EventID: fmt.Sprintf("eval-event-%d", i), Quote: "评测记忆"}},
		})
	}
	if err := s.SaveNotes(ctx, 1, seeded, nil); err != nil {
		t.Fatal(err)
	}
	notes, err := s.ActiveNotes(ctx, 1, total+10)
	if err != nil || len(notes) != total {
		t.Fatalf("seeded notes = %d err=%v", len(notes), err)
	}
	for i, note := range notes {
		vector := []float32{float32(i%7) / 7, float32(i%11) / 11, float32(i%13) / 13}
		if err := s.IndexNote(ctx, note, vector, "v1"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.ExecContext(ctx, `ANALYZE groupfriend.notes`); err != nil {
		t.Fatal(err)
	}
	const explain = `EXPLAIN (ANALYZE, BUFFERS) SELECT id FROM groupfriend.notes WHERE group_id=$1 AND state='active' AND (expires_at IS NULL OR expires_at>NOW()) AND (valid_from IS NULL OR valid_from<=NOW()) AND (valid_to IS NULL OR valid_to>NOW()) AND indexed_revision=revision AND embedding_model=$2 AND embedding IS NOT NULL AND (embedding <=> $3)<$5 ORDER BY embedding <=> $3 LIMIT $4`
	probe := pgvector.NewHalfVector([]float32{0.1, 0.2, 0.3})
	rows, err := s.DB.QueryContext(ctx, explain, 1, "v1", probe, 20, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	defaultPlan := scanPlan(t, rows)
	t.Logf("default plan:\n%s", defaultPlan)
	// Remove the broad B-tree indexes and primary key, then disable sequential
	// and bitmap scans, so the vector index is the only possible path. The
	// default plan on a small table is statistics-dependent; this proves the
	// parameterized threshold query itself is HNSW-compatible.
	for _, statement := range []string{
		`DROP INDEX IF EXISTS groupfriend.friend_notes_scope`,
		`DROP INDEX IF EXISTS groupfriend.friend_notes_topic`,
		`ALTER TABLE groupfriend.notes DROP CONSTRAINT IF EXISTS notes_pkey`,
	} {
		if _, err := s.DB.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.ExecContext(ctx, `ANALYZE groupfriend.notes`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL enable_bitmapscan = off`); err != nil {
		t.Fatal(err)
	}
	rows, err = tx.QueryContext(ctx, explain, 1, "v1", probe, 20, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	plan := scanPlan(t, rows)
	t.Logf("vector-only plan:\n%s", plan)
	if !strings.Contains(plan, "friend_notes_vector") {
		t.Fatalf("vector query did not use the HNSW index:\n%s", plan)
	}
}
