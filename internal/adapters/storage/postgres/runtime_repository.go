package postgresstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

// Working-memory, thought and learning cursor persistence live together
// because they form the runtime projection used to resume background work.
func (s *Store) LoadWorkingMemory(ctx context.Context, groupID int64) (presencedomain.GroupWorkingMemory, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT state_json FROM group_working_memory WHERE group_id = $1`, groupID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return presencedomain.GroupWorkingMemory{GroupID: groupID}, nil
	}
	if err != nil {
		return presencedomain.GroupWorkingMemory{}, err
	}
	var state presencedomain.GroupWorkingMemory
	if err := json.Unmarshal(raw, &state); err != nil {
		return presencedomain.GroupWorkingMemory{}, err
	}
	return state, nil
}

func (s *Store) SaveWorkingMemory(ctx context.Context, state presencedomain.GroupWorkingMemory) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO group_working_memory (group_id, state_json, updated_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (group_id) DO UPDATE SET state_json = EXCLUDED.state_json, updated_at = EXCLUDED.updated_at
	`, state.GroupID, raw, time.Now())
	return err
}

func (s *Store) SaveThought(ctx context.Context, thought replydomain.ThoughtRecord) error {
	evidence, err := json.Marshal(thought.Evidence)
	if err != nil {
		return err
	}
	if thought.CreatedAt.IsZero() {
		thought.CreatedAt = time.Now()
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO thought_records (
			thought_id, candidate_id, group_id, event_id, interpretation, evidence_json,
			uncertainty, chosen_action, outcome, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (thought_id) DO UPDATE SET
			interpretation = EXCLUDED.interpretation, evidence_json = EXCLUDED.evidence_json,
			uncertainty = EXCLUDED.uncertainty, chosen_action = EXCLUDED.chosen_action, outcome = EXCLUDED.outcome
	`, thought.ThoughtID, thought.CandidateID, thought.GroupID, thought.EventID, thought.Interpretation,
		evidence, thought.Uncertainty, thought.ChosenAction, thought.Outcome, thought.CreatedAt)
	return err
}

// RecentThoughts 返回一群最近的思考记录（新到旧），供下轮决策回看。
func (s *Store) RecentThoughts(ctx context.Context, groupID int64, limit int) ([]replydomain.ThoughtRecord, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT thought_id, candidate_id, group_id, event_id, interpretation, evidence_json,
		       uncertainty, chosen_action, outcome, created_at
		FROM thought_records
		WHERE group_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, groupID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := []replydomain.ThoughtRecord{}
	for rows.Next() {
		var (
			thought  replydomain.ThoughtRecord
			evidence []byte
		)
		if err := rows.Scan(
			&thought.ThoughtID, &thought.CandidateID, &thought.GroupID, &thought.EventID,
			&thought.Interpretation, &evidence, &thought.Uncertainty,
			&thought.ChosenAction, &thought.Outcome, &thought.CreatedAt,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(evidence, &thought.Evidence)
		records = append(records, thought)
	}
	return records, rows.Err()
}

func (s *Store) GetLearningWatermark(ctx context.Context, groupID int64, kind string) (memorydomain.LearningWatermark, error) {
	watermark := memorydomain.LearningWatermark{GroupID: groupID, Kind: kind}
	err := s.db.QueryRowContext(ctx, `
		SELECT occurred_at, event_id, updated_at
		FROM learning_watermarks
		WHERE group_id = $1 AND kind = $2
	`, groupID, kind).Scan(&watermark.OccurredAt, &watermark.EventID, &watermark.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return watermark, nil
	}
	return watermark, err
}

func (s *Store) SaveLearningWatermark(ctx context.Context, watermark memorydomain.LearningWatermark) error {
	if watermark.UpdatedAt.IsZero() {
		watermark.UpdatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO learning_watermarks (group_id, kind, occurred_at, event_id, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (group_id, kind) DO UPDATE SET occurred_at = EXCLUDED.occurred_at, event_id = EXCLUDED.event_id, updated_at = EXCLUDED.updated_at
	`, watermark.GroupID, watermark.Kind, watermark.OccurredAt, watermark.EventID, watermark.UpdatedAt)
	return err
}

func (s *Store) UpsertLearningCandidate(ctx context.Context, candidate memorydomain.LearningCandidate) error {
	evidence, err := json.Marshal(candidate.ExampleEventIDs)
	if err != nil {
		return err
	}
	if candidate.CreatedAt.IsZero() {
		candidate.CreatedAt = time.Now()
	}
	if candidate.Status == "" {
		candidate.Status = "staged"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO learning_candidates (
			id, group_id, target_user_id, kind, value, meaning, evidence_count,
			example_event_ids_json, confidence, status, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (id) DO UPDATE SET
			meaning = EXCLUDED.meaning,
			evidence_count = GREATEST(learning_candidates.evidence_count, EXCLUDED.evidence_count),
			example_event_ids_json = EXCLUDED.example_event_ids_json,
			confidence = EXCLUDED.confidence
	`, candidate.ID, candidate.GroupID, candidate.TargetUserID, candidate.Kind, candidate.Value,
		candidate.Meaning, candidate.EvidenceCount, evidence, candidate.Confidence, candidate.Status, candidate.CreatedAt); err != nil {
		return err
	}
	for _, eventID := range candidate.ExampleEventIDs {
		if eventID == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO learning_candidate_evidence (candidate_id, event_id)
			VALUES ($1, $2) ON CONFLICT DO NOTHING
		`, candidate.ID, eventID); err != nil {
			return err
		}
	}
	if len(candidate.ExampleEventIDs) > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE learning_candidates SET evidence_count = GREATEST(evidence_count, (
				SELECT COUNT(*) FROM learning_candidate_evidence WHERE candidate_id = $1
			)) WHERE id = $1
		`, candidate.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListLearningCandidates(ctx context.Context, groupID int64, limit int) ([]memorydomain.LearningCandidate, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, group_id, target_user_id, kind, value, meaning, evidence_count,
		       example_event_ids_json, confidence, status, created_at
		FROM learning_candidates
		WHERE group_id = $1 AND status IN ('staged', 'accepted')
		ORDER BY created_at ASC, id ASC
		LIMIT $2
	`, groupID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]memorydomain.LearningCandidate, 0, limit)
	for rows.Next() {
		var candidate memorydomain.LearningCandidate
		var evidence []byte
		if err := rows.Scan(&candidate.ID, &candidate.GroupID, &candidate.TargetUserID, &candidate.Kind,
			&candidate.Value, &candidate.Meaning, &candidate.EvidenceCount, &evidence,
			&candidate.Confidence, &candidate.Status, &candidate.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(evidence, &candidate.ExampleEventIDs); err != nil {
			return nil, err
		}
		result = append(result, candidate)
	}
	return result, rows.Err()
}

func (s *Store) UpdateLearningCandidateStatus(ctx context.Context, id, status string) error {
	if id == "" || status == "" {
		return errors.New("learning candidate: id and status are required")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE learning_candidates SET status = $1 WHERE id = $2`, status, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errors.New("learning candidate: not found")
	}
	return nil
}
