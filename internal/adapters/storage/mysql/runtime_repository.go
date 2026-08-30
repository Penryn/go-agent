package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/phlin/go-agent/internal/domain/memory"
	"github.com/phlin/go-agent/internal/domain/reply"
	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
)

// Working-memory, thought and learning cursor persistence live together
// because they form the runtime projection used to resume background work.
func (s *Store) LoadWorkingMemory(ctx context.Context, groupID int64) (humandomain.GroupWorkingMemory, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT state_json FROM group_working_memory WHERE group_id = ?`, groupID).Scan(&raw)
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
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE state_json = VALUES(state_json), updated_at = VALUES(updated_at)
	`, state.GroupID, raw, time.Now())
	return err
}

func (s *Store) SaveThought(ctx context.Context, thought reply.ThoughtRecord) error {
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
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			interpretation = VALUES(interpretation), evidence_json = VALUES(evidence_json),
			uncertainty = VALUES(uncertainty), chosen_action = VALUES(chosen_action), outcome = VALUES(outcome)
	`, thought.ThoughtID, thought.CandidateID, thought.GroupID, thought.EventID, thought.Interpretation,
		evidence, thought.Uncertainty, thought.ChosenAction, thought.Outcome, thought.CreatedAt)
	return err
}

func (s *Store) GetLearningWatermark(ctx context.Context, groupID int64, kind string) (memory.LearningWatermark, error) {
	watermark := memory.LearningWatermark{GroupID: groupID, Kind: kind}
	err := s.db.QueryRowContext(ctx, `
		SELECT occurred_at, event_id, updated_at
		FROM learning_watermarks
		WHERE group_id = ? AND kind = ?
	`, groupID, kind).Scan(&watermark.OccurredAt, &watermark.EventID, &watermark.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return watermark, nil
	}
	return watermark, err
}

func (s *Store) SaveLearningWatermark(ctx context.Context, watermark memory.LearningWatermark) error {
	if watermark.UpdatedAt.IsZero() {
		watermark.UpdatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO learning_watermarks (group_id, kind, occurred_at, event_id, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE occurred_at = VALUES(occurred_at), event_id = VALUES(event_id), updated_at = VALUES(updated_at)
	`, watermark.GroupID, watermark.Kind, watermark.OccurredAt, watermark.EventID, watermark.UpdatedAt)
	return err
}
