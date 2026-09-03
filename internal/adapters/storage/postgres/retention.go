package postgresstore

import (
	"context"
	"database/sql"
	"fmt"
)

// PruneObservability removes high-volume operational records while retaining
// messages and memories used by event and knowledge views.
func PruneObservability(ctx context.Context, db *sql.DB, retentionDays int) error {
	if db == nil || retentionDays <= 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin observability cleanup: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"retrieval_traces", "model_usage_records", "thought_records"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE created_at < NOW() - ($1 || ' days')::interval", retentionDays); err != nil {
			return fmt.Errorf("prune %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM async_outbox
		WHERE status IN ('completed', 'dead_letter')
		  AND updated_at < NOW() - ($1 || ' days')::interval
	`, retentionDays); err != nil {
		return fmt.Errorf("prune async_outbox: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit observability cleanup: %w", err)
	}
	return nil
}
