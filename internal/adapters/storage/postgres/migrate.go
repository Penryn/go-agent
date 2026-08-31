package postgresstore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
)

// ApplySchema 执行单个幂等 schema 文件(CREATE ... IF NOT EXISTS),可重复执行。
func ApplySchema(ctx context.Context, db *sql.DB, schemaPath string) error {
	if _, err := db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		return fmt.Errorf("create vector extension: %w", err)
	}

	content, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("read schema file: %w", err)
	}
	for _, statement := range splitStatements(string(content)) {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("exec schema: %w", err)
		}
	}

	return nil
}

// splitStatements 按 ";\n" 切分;PG 方言下 CREATE INDEX 等语句内部不含分号换行,安全。
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
