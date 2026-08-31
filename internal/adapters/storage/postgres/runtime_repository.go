package postgresstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
)

// Working-memory, thought and learning cursor persistence live together
// because they form the runtime projection used to resume background work.
func (s *Store) LoadWorkingMemory(ctx context.Context, groupID int64) (humandomain.GroupWorkingMemory, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT state_json FROM group_working_memory WHERE group_id = $1`, groupID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return humandomain.GroupWorkingMemory{GroupID: groupID}, nil
	}
	if err != nil {
		return humandomain.GroupWorkingMemory{}, err
	}
	var state humandomain.GroupWorkingMemory
	if err := json.Unmarshal(raw, &state); err != nil {
		return humandomain.GroupWorkingMemory{}, err
	}
	return state, nil
}

func (s *Store) SaveWorkingMemory(ctx context.Context, state humandomain.GroupWorkingMemory) error {
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
