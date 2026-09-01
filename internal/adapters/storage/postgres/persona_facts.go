package postgresstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	personadomain "github.com/phlin/go-agent/internal/domain/persona"
)

func (s *Store) AppendPersonaFact(ctx context.Context, fact personadomain.PersonaFact) error {
	return appendPersonaFactExec(ctx, s.db, fact)
}

type personaFactExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func appendPersonaFactExec(ctx context.Context, exec personaFactExecer, fact personadomain.PersonaFact) error {
	var expiresAt any
	if !fact.ExpiresAt.IsZero() {
		expiresAt = fact.ExpiresAt
	}
	_, err := exec.ExecContext(ctx, `
		INSERT INTO persona_fact_events (
			fact_id, persona_id, fact_key, fact_value, status, source_kind,
			source_group_id, source_user_id, source_event_id, supersedes_fact_id,
			definition_hash, resolution_state, confidence, effective_at, expires_at, recorded_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (fact_id) DO NOTHING
	`, fact.FactID, fact.PersonaID, fact.Key, fact.Value, fact.Status, fact.SourceKind,
		fact.SourceGroupID, fact.SourceUserID, fact.SourceEventID, nullableString(fact.SupersedesFactID),
		fact.DefinitionHash, defaultResolutionState(fact.ResolutionState), fact.Confidence, fact.EffectiveAt, expiresAt, fact.RecordedAt)
	return err
}

func (s *Store) CurrentPersonaFacts(ctx context.Context, personaID string, now time.Time) ([]personadomain.PersonaFact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT ON (fact_key, status)
			fact_id, persona_id, fact_key, fact_value, status, source_kind,
			source_group_id, source_user_id, source_event_id, supersedes_fact_id,
			definition_hash, resolution_state, confidence,
			effective_at, expires_at, recorded_at
		FROM persona_fact_events
		WHERE persona_id = $1 AND effective_at <= $2 AND (expires_at IS NULL OR expires_at > $2)
		ORDER BY fact_key, status, effective_at DESC, recorded_at DESC
	`, personaID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var facts []personadomain.PersonaFact
	for rows.Next() {
		var fact personadomain.PersonaFact
		var expiresAt sql.NullTime
		var supersedes sql.NullString
		if err := rows.Scan(
			&fact.FactID, &fact.PersonaID, &fact.Key, &fact.Value, &fact.Status, &fact.SourceKind,
			&fact.SourceGroupID, &fact.SourceUserID, &fact.SourceEventID, &supersedes,
			&fact.DefinitionHash, &fact.ResolutionState, &fact.Confidence,
			&fact.EffectiveAt, &expiresAt, &fact.RecordedAt,
		); err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			fact.ExpiresAt = expiresAt.Time
		}
		fact.SupersedesFactID = supersedes.String
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

func (s *Store) ReservePersonaFacts(ctx context.Context, reservation personadomain.PersonaFactReservation) error {
	if reservation.ReservationID == "" || reservation.PersonaID == "" || len(reservation.Items) == 0 {
		return errors.New("persona fact reservation requires id, persona and items")
	}
	if reservation.ExpiresAt.IsZero() {
		reservation.ExpiresAt = time.Now().Add(time.Minute)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM persona_fact_reservations WHERE expires_at <= $1`, time.Now()); err != nil {
		return err
	}
	for _, item := range reservation.Items {
		var currentID string
		err := tx.QueryRowContext(ctx, `
			SELECT fact_id
			FROM persona_fact_events
			WHERE persona_id = $1 AND fact_key = $2 AND status IN ($3, $4)
			  AND (expires_at IS NULL OR expires_at > $5)
			ORDER BY CASE status WHEN $3 THEN 0 ELSE 1 END, effective_at DESC, recorded_at DESC
			LIMIT 1
			FOR UPDATE
		`, reservation.PersonaID, item.Key, personadomain.PersonaFactVerified, personadomain.PersonaFactCanon, time.Now()).Scan(&currentID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if currentID != item.ExpectedFactID {
			return fmt.Errorf("%w: key=%s expected=%s current=%s", personadomain.ErrFactReservationConflict, item.Key, item.ExpectedFactID, currentID)
		}
		result, err := tx.ExecContext(ctx, `
			INSERT INTO persona_fact_reservations (
				reservation_id, persona_id, fact_key, fact_value, expected_fact_id,
				definition_hash, expires_at, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (persona_id, fact_key) DO NOTHING
		`, reservation.ReservationID, reservation.PersonaID, item.Key, item.Value, item.ExpectedFactID,
			reservation.DefinitionHash, reservation.ExpiresAt, time.Now())
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return fmt.Errorf("%w: key=%s is already reserved", personadomain.ErrFactReservationConflict, item.Key)
		}
	}
	return tx.Commit()
}

func (s *Store) FinalizePersonaFacts(ctx context.Context, reservationID string, facts []personadomain.PersonaFact) error {
	if reservationID == "" {
		return errors.New("persona fact reservation id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, fact := range facts {
		if err := appendPersonaFactExec(ctx, tx, fact); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM persona_fact_reservations WHERE reservation_id = $1`, reservationID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReleasePersonaFacts(ctx context.Context, reservationID string) error {
	if reservationID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM persona_fact_reservations WHERE reservation_id = $1`, reservationID)
	return err
}

func defaultResolutionState(value string) string {
	if value == "" {
		return personadomain.FactResolutionActive
	}
	return value
}
