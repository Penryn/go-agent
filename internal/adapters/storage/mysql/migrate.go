package mysqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ApplyMigrations(ctx context.Context, db *sql.DB, migrationsDir string) error {
	content, err := os.ReadFile(filepath.Join(migrationsDir, "001_init.sql"))
	if err != nil {
		return fmt.Errorf("read migration: %w", err)
	}

	for _, statement := range splitStatements(string(content)) {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("exec migration statement: %w", err)
		}
	}

	return nil
}

func splitStatements(sqlText string) []string {
	chunks := strings.Split(sqlText, ";\n")
	statements := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk != "" {
			statements = append(statements, chunk)
		}
	}
	return statements
}
