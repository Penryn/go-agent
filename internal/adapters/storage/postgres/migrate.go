package postgresstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// ApplySchema 执行单个幂等 schema 文件(CREATE ... IF NOT EXISTS),可重复执行。
func ApplySchema(ctx context.Context, db *sql.DB, schemaPath string) error {
	if _, err := db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		return fmt.Errorf("create vector extension: %w", err)
	}
	if err := migrateLegacyVectorTables(ctx, db); err != nil {
		return err
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

// migrateLegacyVectorTables preserves pre-halfvec tables so the current schema
// can be recreated with the configured embedding dimension. Existing vectors
// were truncated to 2000 dimensions and cannot be losslessly converted; they
// must be regenerated from the authoritative records.
func migrateLegacyVectorTables(ctx context.Context, db *sql.DB) error {
	for _, table := range []string{"memory_vectors", "meme_vectors"} {
		var dataType string
		err := db.QueryRowContext(ctx, `
			SELECT COALESCE(udt_name, '')
			FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = 'embedding'
		`, table).Scan(&dataType)
		if errors.Is(err, sql.ErrNoRows) || dataType == "" || dataType == "halfvec" {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect %s embedding schema: %w", table, err)
		}
		if dataType != "vector" {
			return fmt.Errorf("unsupported %s embedding type %q", table, dataType)
		}
		suffix := fmt.Sprintf("legacy_%d", time.Now().UnixNano())
		indexNames := []string{table + "_pkey", "idx_" + table + "_embedding"}
		if table == "meme_vectors" {
			indexNames = append(indexNames, "idx_meme_vectors_group")
		}
		for _, index := range indexNames {
			if _, err := db.ExecContext(ctx, fmt.Sprintf("ALTER INDEX IF EXISTS %s RENAME TO %s_%s", index, index, suffix)); err != nil {
				return fmt.Errorf("preserve %s index %s: %w", table, index, err)
			}
		}
		legacy := fmt.Sprintf("%s_%s", table, suffix)
		if _, err := db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s RENAME TO %s", table, legacy)); err != nil {
			return fmt.Errorf("preserve %s table: %w", table, err)
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
