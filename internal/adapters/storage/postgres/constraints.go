package postgresstore

import (
	"context"
	"database/sql"

	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

// GetConstraints reads structured preferences from the same authoritative
// memories table used by explicit memory, learning and retrieval.
func (s *Store) GetConstraints(ctx context.Context, scope string, targetIDs []string) ([]memorydomain.MemoryConstraint, error) {
	if len(targetIDs) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT ON (subject, predicate, qualifier)
		       subject, predicate, normalized_value, qualifier, expires_at
		FROM memories
		WHERE scope = $1 AND subject = ANY($2)
		  AND predicate IN ('preferred_name', 'allow_mention', 'allow_poke')
		  AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY subject, predicate, qualifier, created_at DESC, memory_id DESC
	`, scope, targetIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var constraints []memorydomain.MemoryConstraint
	for rows.Next() {
		var constraint memorydomain.MemoryConstraint
		var expiresAt sql.NullTime
		if err := rows.Scan(&constraint.SubjectID, &constraint.Type, &constraint.Value, &constraint.Qualifier, &expiresAt); err != nil {
			return nil, err
		}
		constraint.SubjectKind = memorydomain.SubjectKindUser
		if expiresAt.Valid {
			constraint.ValidUntil = &expiresAt.Time
		}
		constraints = append(constraints, constraint)
	}
	return constraints, rows.Err()
}
