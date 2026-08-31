package postgresstore

import (
	"context"
	"path/filepath"
	"testing"
)

func TestApplyMigrations(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, "postgres://qqbot:qqbotpass@127.0.0.1:5432/qqbot?sslmode=disable")
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer db.Close()

	if err := ApplyMigrations(ctx, db, filepath.Join("..", "..", "..", "..", "migrations")); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	// 幂等:重复应用不报错
	if err := ApplyMigrations(ctx, db, filepath.Join("..", "..", "..", "..", "migrations")); err != nil {
		t.Fatalf("re-apply migrations: %v", err)
	}

	var tables int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public'`).Scan(&tables); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	// 12 张业务表 + schema_migrations = 13
	if tables < 12 {
		t.Fatalf("expected at least 12 tables, got %d", tables)
	}
}
