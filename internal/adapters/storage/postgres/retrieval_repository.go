package postgresstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/phlin/go-agent/internal/application/ports"
)

func (s *Store) SaveRetrievalTrace(ctx context.Context, trace ports.RetrievalTrace) error {
	if trace.TraceID == "" {
		trace.TraceID = fmt.Sprintf("retrieval-%d", time.Now().UnixNano())
	}
	if trace.CreatedAt.IsZero() {
		trace.CreatedAt = time.Now()
	}
	hits, err := json.Marshal(nonNilStrings(trace.HitMemoryIDs))
	if err != nil {
		return err
	}
	selected, err := json.Marshal(nonNilStrings(trace.SelectedIDs))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO retrieval_traces (
			trace_id, event_id, group_id, user_id, query, candidate_count,
			hit_memory_ids_json, selected_memory_ids_json, outcome, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (trace_id) DO UPDATE SET
			query = EXCLUDED.query, candidate_count = EXCLUDED.candidate_count,
			hit_memory_ids_json = EXCLUDED.hit_memory_ids_json
	`, trace.TraceID, trace.EventID, trace.GroupID, trace.UserID, trace.Query,
		trace.CandidateCount, hits, selected, trace.Outcome, trace.CreatedAt)
	return err
}

func (s *Store) UpdateRetrievalTrace(ctx context.Context, eventID string, selectedIDs []string, outcome string) error {
	if eventID == "" {
		return nil
	}
	selected, err := json.Marshal(nonNilStrings(selectedIDs))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE retrieval_traces
		SET selected_memory_ids_json = $1, outcome = $2
		WHERE event_id = $3 AND created_at > NOW() - INTERVAL '10 minutes'
	`, selected, outcome, eventID)
	return err
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
