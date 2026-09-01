package postgresstore

import (
	"context"
	"database/sql"
	"time"

	personadomain "github.com/phlin/go-agent/internal/domain/persona"
)

func (s *Store) AppendPersonaFact(ctx context.Context, fact personadomain.PersonaFact) error {
	var expiresAt any
	if !fact.ExpiresAt.IsZero() {
		expiresAt = fact.ExpiresAt
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO persona_fact_events (
			fact_id, persona_id, fact_key, fact_value, status, source_kind,
			source_group_id, source_user_id, source_event_id, confidence,
			effective_at, expires_at, recorded_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (fact_id) DO NOTHING
	`, fact.FactID, fact.PersonaID, fact.Key, fact.Value, fact.Status, fact.SourceKind,
		fact.SourceGroupID, fact.SourceUserID, fact.SourceEventID, fact.Confidence,
		fact.EffectiveAt, expiresAt, fact.RecordedAt)
	return err
}

func (s *Store) CurrentPersonaFacts(ctx context.Context, personaID string, now time.Time) ([]personadomain.PersonaFact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT ON (fact_key, status)
			fact_id, persona_id, fact_key, fact_value, status, source_kind,
			source_group_id, source_user_id, source_event_id, confidence,
			effective_at, expires_at, recorded_at
		FROM persona_fact_events
		WHERE persona_id = $1 AND (expires_at IS NULL OR expires_at > $2)
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
		if err := rows.Scan(
			&fact.FactID, &fact.PersonaID, &fact.Key, &fact.Value, &fact.Status, &fact.SourceKind,
			&fact.SourceGroupID, &fact.SourceUserID, &fact.SourceEventID, &fact.Confidence,
			&fact.EffectiveAt, &expiresAt, &fact.RecordedAt,
		); err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			fact.ExpiresAt = expiresAt.Time
		}
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}
