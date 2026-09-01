// Package testsupport 提供跨包集成测试共用的 Postgres 测试库搭建。
// PG 不可用时测试 skip 而非失败,与 postgresstore 自身测试同一约定。
package testsupport

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	postgresstore "github.com/phlin/go-agent/internal/adapters/storage/postgres"
)

// NewStore 为每个测试创建独立的临时数据库并套用 schema,返回已就绪的
// postgresstore.Store。测试结束自动 drop。
func NewStore(t *testing.T) *postgresstore.Store {
	t.Helper()
	db := NewDB(t)
	return postgresstore.NewStore(db)
}

// NewDB 与 NewStore 相同但返回裸 *sql.DB,供 StateStore / VectorStore 等
// 共用同一连接池的测试使用。
func NewDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()

	adminDB, err := postgresstore.Open(ctx, "postgres://qqbot:qqbotpass@127.0.0.1:5432/postgres?sslmode=disable")
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	testDB := fmt.Sprintf("qqbot_test_%d", time.Now().UnixNano())
	if _, err := adminDB.ExecContext(ctx, "CREATE DATABASE "+testDB); err != nil {
		_ = adminDB.Close()
		t.Fatalf("create test database: %v", err)
	}
	_ = adminDB.Close()

	db, err := postgresstore.Open(ctx, fmt.Sprintf("postgres://qqbot:qqbotpass@127.0.0.1:5432/%s?sslmode=disable", testDB))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		cleanup, err := sql.Open("pgx", "postgres://qqbot:qqbotpass@127.0.0.1:5432/postgres?sslmode=disable")
		if err == nil {
			_, _ = cleanup.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+testDB+" WITH (FORCE)")
			_ = cleanup.Close()
		}
	})

	_, thisFile, _, _ := runtime.Caller(0)
	schemaPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "schema", "schema.sql")
	if err := postgresstore.ApplySchema(ctx, db, schemaPath); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return db
}
