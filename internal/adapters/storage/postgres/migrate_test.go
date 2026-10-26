package postgresstore

import (
	"context"
	"path/filepath"
	"testing"
)

func TestApplySchema(t *testing.T) {
	ctx := context.Background()
	// Never migrate the developer's running qqbot database during a test.
	// setupPostgres creates an isolated database and applies the schema once.
	db := setupPostgres(t)
	// 幂等:重复应用不报错
	if err := ApplySchema(ctx, db, filepath.Join("..", "..", "..", "..", "schema", "schema.sql")); err != nil {
		t.Fatalf("re-apply schema: %v", err)
	}

	var tables int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public'`).Scan(&tables); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	// 12 张业务表(含向量表);schema_migrations 已随多文件迁移机制删除
	if tables < 12 {
		t.Fatalf("expected at least 12 tables, got %d", tables)
	}
}
