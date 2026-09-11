package postgresstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/phlin/go-agent/internal/application/ports"
	relationshipdomain "github.com/phlin/go-agent/internal/domain/relationship"
	scenedomain "github.com/phlin/go-agent/internal/domain/scene"
)

var (
	_ ports.RelationshipStore = (*Store)(nil)
	_ ports.GroupSceneStore   = (*Store)(nil)
)

func (s *Store) GetSocialRelationship(ctx context.Context, personaID string, groupID, userID int64) (relationshipdomain.State, error) {
	state := relationshipdomain.NewState(personaID, groupID, userID)
	err := s.db.QueryRowContext(ctx, `
		SELECT familiarity, affinity, tease_tolerance, trust, friction, last_interact_at, revision, updated_at
		FROM relationships WHERE persona_id = $1 AND group_id = $2 AND user_id = $3
	`, personaID, groupID, userID).Scan(
		&state.Familiarity, &state.Affinity, &state.TeaseTolerance, &state.Trust,
		&state.Friction, &state.LastInteractAt, &state.Revision, &state.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return relationshipdomain.State{}, err
	}
	return state, nil
}

func (s *Store) ApplyRelationshipEvent(ctx context.Context, event relationshipdomain.Event) (bool, error) {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	lockKey := fmt.Sprintf("%s:%d:%d", event.PersonaID, event.GroupID, event.UserID)
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, lockKey); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO relationship_events (
			event_id, persona_id, group_id, user_id, kind, valence, intensity,
			reason, evidence_event_id, decision_id, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (event_id) DO NOTHING
	`, event.EventID, event.PersonaID, event.GroupID, event.UserID, event.Kind,
		event.Valence, event.Intensity, event.Reason, event.EvidenceEventID,
		event.DecisionID, event.CreatedAt)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 0 {
		if err == nil {
			err = tx.Commit()
		}
		return false, err
	}
	state := relationshipdomain.NewState(event.PersonaID, event.GroupID, event.UserID)
	err = tx.QueryRowContext(ctx, `
		SELECT familiarity, affinity, tease_tolerance, trust, friction, last_interact_at, revision, updated_at
		FROM relationships WHERE persona_id = $1 AND group_id = $2 AND user_id = $3 FOR UPDATE
	`, event.PersonaID, event.GroupID, event.UserID).Scan(
		&state.Familiarity, &state.Affinity, &state.TeaseTolerance, &state.Trust,
		&state.Friction, &state.LastInteractAt, &state.Revision, &state.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return false, err
	}
	state = relationshipdomain.Apply(state, event, time.Now())
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now()
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO relationships (
			persona_id, group_id, user_id, familiarity, affinity, tease_tolerance,
			last_interact_at, trust, friction, revision, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (persona_id, group_id, user_id) DO UPDATE SET
			familiarity = EXCLUDED.familiarity,
			affinity = EXCLUDED.affinity,
			tease_tolerance = EXCLUDED.tease_tolerance,
			last_interact_at = EXCLUDED.last_interact_at,
			trust = EXCLUDED.trust,
			friction = EXCLUDED.friction,
			revision = EXCLUDED.revision,
			updated_at = EXCLUDED.updated_at
	`, state.PersonaID, state.GroupID, state.UserID, state.Familiarity, state.Affinity,
		state.TeaseTolerance, state.LastInteractAt, state.Trust, state.Friction,
		state.Revision, state.UpdatedAt)
	if err != nil {
		return false, err
	}

	// 保存历史快照
	_, err = tx.ExecContext(ctx, `
		INSERT INTO relationship_history (
			persona_id, group_id, user_id, revision, familiarity, affinity, trust,
			tease_tolerance, friction, trigger_event_id, trigger_kind, snapshot_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (persona_id, group_id, user_id, revision) DO NOTHING
	`, state.PersonaID, state.GroupID, state.UserID, state.Revision,
		state.Familiarity, state.Affinity, state.Trust, state.TeaseTolerance, state.Friction,
		event.EventID, event.Kind, state.UpdatedAt)
	if err != nil {
		return false, err
	}

	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) LoadGroupScene(ctx context.Context, groupID int64) (scenedomain.GroupScene, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT scene_json FROM group_scenes WHERE group_id = $1`, groupID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return scenedomain.New(groupID), nil
	}
	if err != nil {
		return scenedomain.GroupScene{}, err
	}
	var scene scenedomain.GroupScene
	if err := json.Unmarshal(raw, &scene); err != nil {
		return scenedomain.GroupScene{}, err
	}
	return scene, nil
}

func (s *Store) SaveGroupScene(ctx context.Context, scene scenedomain.GroupScene) error {
	if scene.UpdatedAt.IsZero() {
		scene.UpdatedAt = time.Now()
	}
	raw, err := json.Marshal(scene)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO group_scenes (group_id, scene_json, revision, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (group_id) DO UPDATE SET
			scene_json = EXCLUDED.scene_json,
			revision = EXCLUDED.revision,
			updated_at = EXCLUDED.updated_at
	`, scene.GroupID, raw, scene.Revision, scene.UpdatedAt)
	return err
}
