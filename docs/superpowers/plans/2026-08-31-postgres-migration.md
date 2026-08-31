# PostgreSQL + pgvector 存储迁移实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 MySQL(关系数据)和 Qdrant(向量)替换为一个 PostgreSQL 17 + pgvector 实例,删除旧栈全部代码与基础设施。

**Architecture:** 新建 `internal/adapters/storage/postgres` 包(store.go 关系表 + vector.go 向量表 + migrate.go 迁移器),从 `mysqlstore` 改写 SQL 方言(`ON DUPLICATE KEY` → `ON CONFLICT`),从 `qdrantstore` 重写向量层(自调 embedder + `<=>` 检索,不依赖 eino-ext)。装配点 `dependencies.go`/`graphs.go` 各改一处。三阶段:阶段 A 替 MySQL、阶段 B 替 Qdrant、阶段 C 删旧栈。

**Tech Stack:** Go 1.25、`github.com/jackc/pgx/v5`(database/sql 兼容层)、`github.com/pgvector/pgvector-go`、`pgvector/pgvector:pg17` Docker 镜像。

**关键约定(全程适用):**
- eino `embedding.Embedder` 接口保留:`EmbedStrings(ctx, texts []string, opts ...embedding.Option) ([][]float64, error)`
- pgvector-go 的 `pgvector.Vector` 根包直接实现 `sql.Scanner`/`driver.Valuer`,与 database/sql 兼容,**不需要** pgx 子包的 RegisterTypes(那是原生 pgx 连接才需要的);经 `pgx/v5/stdlib` 注册的 driver 名为 `pgx`
- `<=>` 是余弦**距离**(0=相同),相似度 = `1 - distance`
- 每个任务结束跑 `go build ./...`,每个阶段结束跑 `go build ./... && go vet ./... && go test ./...`
- 集成测试连不上本地 PG(127.0.0.1:5432)自动 skip,不阻塞 CI

**外部依赖前置:** docker daemon 需运行(Task 1 起 PG 容器)。当前 daemon 未启动时,先让用户启动 Docker Desktop。

---

## 阶段 A:PostgreSQL 替换 MySQL

### Task 1: 起 PG 容器 + 依赖引入

**Files:**
- Modify: `docker-compose.yml`(追加 postgres 服务,暂不删旧服务)
- Modify: `go.mod` / `go.sum`(经 go get)

- [ ] **Step 1: docker-compose.yml 追加 postgres 服务**

在 `services:` 下 `mysql:` 服务之前插入(pg17 镜像自带 pgvector):

```yaml
  postgres:
    image: pgvector/pgvector:pg17
    container_name: qqbot-postgres
    restart: unless-stopped
    environment:
      TZ: ${TZ:-Asia/Shanghai}
      POSTGRES_USER: ${POSTGRES_USER:-qqbot}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:-qqbotpass}
      POSTGRES_DB: ${POSTGRES_DB:-qqbot}
    ports:
      - "127.0.0.1:5432:5432"
    volumes:
      - ./data/postgres:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U $${POSTGRES_USER:-qqbot} -d $${POSTGRES_DB:-qqbot}"]
      interval: 10s
      timeout: 5s
      retries: 10
      start_period: 10s
    networks:
      - qqbot-net
```

- [ ] **Step 2: 启动并验证**

```bash
docker compose up -d postgres
docker compose exec postgres psql -U qqbot -d qqbot -c "CREATE EXTENSION IF NOT EXISTS vector; SELECT extversion FROM pg_extension WHERE extname='vector';"
```
Expected: 输出 pgvector 版本号(如 `0.8.0`)。

- [ ] **Step 3: 引入依赖**

```bash
go get github.com/jackc/pgx/v5@v5.10.0
go get github.com/pgvector/pgvector-go@v0.4.1
go mod tidy
```
Expected: `go.mod` require 块出现两个新依赖。

- [ ] **Step 4: Commit**

```bash
git add docker-compose.yml go.mod go.sum
git commit -m "build(deps): add postgres service and pgx/pgvector deps"
```

### Task 2: PG 迁移文件(关系表)

**Files:**
- Create: `migrations/001_init.sql`(**覆盖**现有 MySQL 版本——旧栈数据已确认从零开始,无需保留)
- Delete: `migrations/002_scope_outbox_idempotency.sql`(其变更已并入新 001 的表定义)

- [ ] **Step 1: 重写 migrations/001_init.sql**

整个文件替换为:

```sql
CREATE TABLE IF NOT EXISTS messages (
  event_id VARCHAR(128) PRIMARY KEY,
  group_id BIGINT NOT NULL,
  user_id BIGINT NOT NULL,
  message_id VARCHAR(128) NOT NULL,
  reply_to_message_id VARCHAR(128) NULL,
  kind VARCHAR(32) NOT NULL,
  text_content TEXT NOT NULL,
  segments_json JSONB NOT NULL,
  attachments_json JSONB NOT NULL,
  mentioned_bot BOOLEAN NOT NULL DEFAULT FALSE,
  named_bot BOOLEAN NOT NULL DEFAULT FALSE,
  is_reply_to_bot BOOLEAN NOT NULL DEFAULT FALSE,
  occurred_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_group_occurred ON messages (group_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_messages_message_id ON messages (message_id);

CREATE TABLE IF NOT EXISTS memories (
  memory_id VARCHAR(128) PRIMARY KEY,
  scope VARCHAR(128) NOT NULL,
  type VARCHAR(64) NOT NULL,
  subject VARCHAR(255) NOT NULL,
  content TEXT NOT NULL,
  source_event_id VARCHAR(128) NOT NULL,
  descriptor_ref VARCHAR(255) NOT NULL,
  confidence DOUBLE PRECISION NOT NULL,
  importance DOUBLE PRECISION NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  expires_at TIMESTAMPTZ NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_memories_scope_type ON memories (scope, type);
CREATE INDEX IF NOT EXISTS idx_memories_created ON memories (created_at);

CREATE TABLE IF NOT EXISTS member_profiles (
  group_id BIGINT NOT NULL,
  user_id BIGINT NOT NULL,
  nickname VARCHAR(255) NOT NULL,
  message_count BIGINT NOT NULL,
  last_spoke_at TIMESTAMPTZ NOT NULL,
  active_score DOUBLE PRECISION NOT NULL,
  tags_json JSONB NOT NULL,
  common_phrases_json JSONB NOT NULL,
  interests_json JSONB NOT NULL,
  traits_json JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (group_id, user_id)
);

CREATE TABLE IF NOT EXISTS relationships (
  persona_id VARCHAR(128) NOT NULL,
  group_id BIGINT NOT NULL,
  user_id BIGINT NOT NULL,
  familiarity DOUBLE PRECISION NOT NULL,
  affinity DOUBLE PRECISION NOT NULL,
  tease_tolerance DOUBLE PRECISION NOT NULL,
  grudge_score DOUBLE PRECISION NOT NULL,
  last_interact_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (persona_id, group_id, user_id)
);

CREATE TABLE IF NOT EXISTS meme_assets (
  meme_id VARCHAR(128) PRIMARY KEY,
  group_id BIGINT NOT NULL,
  source_event_id VARCHAR(128) NOT NULL,
  object_key VARCHAR(255) NOT NULL,
  file_ext VARCHAR(32) NOT NULL,
  content_hash VARCHAR(128) NOT NULL,
  perceptual_hash VARCHAR(128) NOT NULL,
  width INT NOT NULL,
  height INT NOT NULL,
  animated BOOLEAN NOT NULL DEFAULT FALSE,
  status VARCHAR(32) NOT NULL,
  send_count BIGINT NOT NULL DEFAULT 0,
  last_sent_at TIMESTAMPTZ NULL,
  created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_meme_assets_group ON meme_assets (group_id);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_meme_content_hash ON meme_assets (content_hash);

CREATE TABLE IF NOT EXISTS meme_descriptors (
  meme_id VARCHAR(128) PRIMARY KEY,
  title VARCHAR(255) NOT NULL,
  summary TEXT NOT NULL,
  keywords_json JSONB NOT NULL,
  emotion_tags_json JSONB NOT NULL,
  scene_tags_json JSONB NOT NULL,
  usage_hints_json JSONB NOT NULL,
  language VARCHAR(32) NOT NULL,
  confidence DOUBLE PRECISION NOT NULL,
  reviewed BOOLEAN NOT NULL DEFAULT FALSE,
  updated_at TIMESTAMPTZ NOT NULL,
  CONSTRAINT fk_meme_descriptor_asset FOREIGN KEY (meme_id) REFERENCES meme_assets(meme_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS learning_candidates (
  id VARCHAR(128) PRIMARY KEY,
  group_id BIGINT NOT NULL,
  kind VARCHAR(64) NOT NULL,
  value TEXT NOT NULL,
  meaning TEXT NOT NULL,
  evidence_count INT NOT NULL,
  example_event_ids_json JSONB NOT NULL,
  confidence DOUBLE PRECISION NOT NULL,
  status VARCHAR(32) NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS learning_watermarks (
  group_id BIGINT NOT NULL,
  kind VARCHAR(64) NOT NULL,
  occurred_at TIMESTAMPTZ NOT NULL,
  event_id VARCHAR(128) NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (group_id, kind)
);

CREATE TABLE IF NOT EXISTS group_working_memory (
  group_id BIGINT PRIMARY KEY,
  state_json JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_group_working_memory_updated ON group_working_memory (updated_at);

CREATE TABLE IF NOT EXISTS thought_records (
  thought_id VARCHAR(128) PRIMARY KEY,
  candidate_id VARCHAR(128) NOT NULL,
  group_id BIGINT NOT NULL,
  event_id VARCHAR(128) NOT NULL,
  interpretation TEXT NOT NULL,
  evidence_json JSONB NOT NULL,
  uncertainty DOUBLE PRECISION NOT NULL,
  chosen_action VARCHAR(64) NOT NULL,
  outcome VARCHAR(64) NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_thought_records_group_created ON thought_records (group_id, created_at);

CREATE TABLE IF NOT EXISTS async_outbox (
  task_id VARCHAR(128) PRIMARY KEY,
  kind VARCHAR(64) NOT NULL,
  idempotency_key VARCHAR(255) NOT NULL,
  payload_json JSONB NOT NULL,
  status VARCHAR(32) NOT NULL,
  attempts INT NOT NULL DEFAULT 0,
  max_attempts INT NOT NULL DEFAULT 5,
  available_at TIMESTAMPTZ NOT NULL,
  locked_until TIMESTAMPTZ NULL,
  locked_by VARCHAR(128) NULL,
  last_error TEXT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_async_outbox_idempotency ON async_outbox (kind, idempotency_key);
CREATE INDEX IF NOT EXISTS idx_async_outbox_claim ON async_outbox (status, available_at, locked_until);
CREATE INDEX IF NOT EXISTS idx_async_outbox_updated ON async_outbox (updated_at);
```

注意:`uniq_async_outbox_idempotency` 直接建为 `(kind, idempotency_key)` 复合唯一索引——旧 MySQL 002 迁移的最终形态。

- [ ] **Step 2: 删除旧迁移文件**

```bash
rm migrations/002_scope_outbox_idempotency.sql
```

- [ ] **Step 3: Commit**

```bash
git add migrations/
git commit -m "feat(migrations): rewrite schema in PostgreSQL dialect"
```

### Task 3: postgres 包骨架 + 迁移器

**Files:**
- Create: `internal/adapters/storage/postgres/migrate.go`
- Create: `internal/adapters/storage/postgres/store.go`(仅 Open/NewStore)

- [ ] **Step 1: 写 migrate.go**

```go
package postgresstore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ApplyMigrations(ctx context.Context, db *sql.DB, migrationsDir string) error {
	if _, err := db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		return fmt.Errorf("create vector extension: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version VARCHAR(255) PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("create migration history: %w", err)
	}

	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("read migrations directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		var applied int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = $1`, entry.Name()).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %q: %w", entry.Name(), err)
		}
		if applied > 0 {
			continue
		}
		content, err := os.ReadFile(filepath.Join(migrationsDir, entry.Name()))
		if err != nil {
			return fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		for _, statement := range splitStatements(string(content)) {
			if _, err := db.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("exec migration %q: %w", entry.Name(), err)
			}
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, entry.Name()); err != nil {
			return fmt.Errorf("record migration %q: %w", entry.Name(), err)
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
```

- [ ] **Step 2: 写 store.go 骨架**

```go
package postgresstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/phlin/go-agent/internal/core/ports"
)

var (
	_ ports.MemoryStore        = (*Store)(nil)
	_ ports.LearningStateStore = (*Store)(nil)
	_ ports.ThoughtStore       = (*Store)(nil)
	_ ports.MemeStore          = (*Store)(nil)
	_ ports.ProfileStore       = (*Store)(nil)
	_ ports.OutboxStore        = (*Store)(nil)
)

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return db, nil
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}
```

(接口断言此时会编译失败——Store 还没实现方法。临时把 `var _ ports...` 断言块注释掉,Task 4-6 逐个恢复。)

- [ ] **Step 3: 编译验证**

```bash
go build ./internal/adapters/storage/postgres/
```
Expected: 成功(断言块注释状态下)。

- [ ] **Step 4: Commit**

```bash
git add internal/adapters/storage/postgres/
git commit -m "feat(postgres): package skeleton and migrator"
```

### Task 4: 集成测试先行(outbox + events + memories)

**Files:**
- Create: `internal/adapters/storage/postgres/store_test.go`
- Modify: `internal/adapters/storage/postgres/store.go`(恢复 outbox/events/memories 断言,实现对应方法)

- [ ] **Step 1: 写 store_test.go(第一部分)**

```go
package postgresstore

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
)

func setupPostgres(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()

	adminDB, err := Open(ctx, "postgres://qqbot:qqbotpass@127.0.0.1:5432/postgres?sslmode=disable")
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	testDB := fmt.Sprintf("qqbot_test_%d", time.Now().UnixNano())
	if _, err := adminDB.ExecContext(ctx, "CREATE DATABASE "+testDB); err != nil {
		_ = adminDB.Close()
		t.Fatalf("create test database: %v", err)
	}
	_ = adminDB.Close()

	db, err := Open(ctx, fmt.Sprintf("postgres://qqbot:qqbotpass@127.0.0.1:5432/%s?sslmode=disable", testDB))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		cleanup, err := Open(ctx, "postgres://qqbot:qqbotpass@127.0.0.1:5432/postgres?sslmode=disable")
		if err == nil {
			_, _ = cleanup.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+testDB+" WITH (FORCE)")
			_ = cleanup.Close()
		}
	})

	if err := ApplyMigrations(ctx, db, filepath.Join("..", "..", "..", "..", "migrations")); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return db
}

func TestOutboxRoundtrip(t *testing.T) {
	ctx := context.Background()
	db := setupPostgres(t)
	store := NewStore(db)

	task := ports.OutboxTask{
		ID:             fmt.Sprintf("task-%d", time.Now().UnixNano()),
		Kind:           "memory_vector_index",
		IdempotencyKey: fmt.Sprintf("idem-%d", time.Now().UnixNano()),
		Payload:        []byte(`{"memory_id":"m1"}`),
	}
	if err := store.EnqueueOutbox(ctx, task); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	// 同 idempotency key 重复入队应幂等(不报错、不新增)
	if err := store.EnqueueOutbox(ctx, task); err != nil {
		t.Fatalf("idempotent enqueue: %v", err)
	}

	claimed, err := store.ClaimOutbox(ctx, "worker-1", time.Now(), time.Minute, 5)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("expected 1 claimed task, got %d", len(claimed))
	}
	if claimed[0].Status != ports.OutboxRunning || claimed[0].LockedBy != "worker-1" {
		t.Fatalf("unexpected claim state: %+v", claimed[0])
	}
	// 已锁定的任务不应被再次领取
	again, err := store.ClaimOutbox(ctx, "worker-2", time.Now(), time.Minute, 5)
	if err != nil {
		t.Fatalf("claim again: %v", err)
	}
	for _, c := range again {
		if c.ID == task.ID {
			t.Fatalf("locked task should not be reclaimed")
		}
	}
	if err := store.CompleteOutbox(ctx, task.ID); err != nil {
		t.Fatalf("complete: %v", err)
	}
}

func TestEventsAndMemories(t *testing.T) {
	ctx := context.Background()
	db := setupPostgres(t)
	store := NewStore(db)

	event := conversationdomain.ConversationEvent{
		EventID:       fmt.Sprintf("event-%d", time.Now().UnixNano()),
		GroupID:       1,
		UserID:        2,
		MessageID:     "m-1",
		Kind:          conversationdomain.EventMessage,
		Text:          "hello",
		Segments:      []conversationdomain.MessageSegment{{Type: "text", Data: map[string]any{"text": "hello"}}},
		TimestampUnix: time.Now().Unix(),
	}
	if err := store.ArchiveEvent(ctx, event); err != nil {
		t.Fatalf("archive event: %v", err)
	}
	// 同 event_id 重复归档应 upsert 而非报错
	if err := store.ArchiveEvent(ctx, event); err != nil {
		t.Fatalf("idempotent archive: %v", err)
	}
	events, err := store.RecentEvents(ctx, 1, 5)
	if err != nil {
		t.Fatalf("recent events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	after, err := store.EventsAfter(ctx, 1, time.Now().Add(-time.Hour), "", 10)
	if err != nil {
		t.Fatalf("events after: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("expected 1 event after, got %d", len(after))
	}

	record := memorydomain.MemoryRecord{
		MemoryID:      fmt.Sprintf("memory-%d", time.Now().UnixNano()),
		Scope:         "group:1",
		Type:          "preference",
		Subject:       "梗",
		Content:       "这个群爱聊旧梗",
		SourceEventID: event.EventID,
		Confidence:    0.9,
		Importance:    0.8,
		CreatedAt:     time.Now(),
	}
	if err := store.UpsertMemory(ctx, record); err != nil {
		t.Fatalf("upsert memory: %v", err)
	}
	records, err := store.QueryMemories(ctx, ports.MemoryQuery{Scope: "group:1", Query: "旧梗", TopK: 3})
	if err != nil {
		t.Fatalf("query memories: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(records))
	}
}

func TestProfiles(t *testing.T) {
	ctx := context.Background()
	db := setupPostgres(t)
	store := NewStore(db)

	profile := profiledomain.MemberProfile{
		Stats: profiledomain.MemberStats{
			GroupID: 1, UserID: 2, Nickname: "alice",
			MessageCount: 7, LastSpokeAt: time.Now(), ActiveScore: 0.8,
		},
		Tags: []string{"老群友"},
	}
	if err := store.SaveMemberProfile(ctx, profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	saved, err := store.GetMemberProfile(ctx, 1, 2)
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	if saved.Stats.Nickname != "alice" || len(saved.Tags) != 1 {
		t.Fatalf("unexpected profile: %+v", saved)
	}

	state := profiledomain.RelationshipState{
		PersonaID: "main", GroupID: 1, UserID: 2,
		Familiarity: 0.3, Affinity: 0.25, TeaseTolerance: 0.5, GrudgeScore: 0,
		LastInteractAt: time.Now(),
	}
	if err := store.SaveRelationship(ctx, state); err != nil {
		t.Fatalf("save relationship: %v", err)
	}
	got, err := store.GetRelationship(ctx, "main", 1, 2)
	if err != nil {
		t.Fatalf("get relationship: %v", err)
	}
	if got.Affinity != 0.25 {
		t.Fatalf("unexpected affinity: %f", got.Affinity)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/adapters/storage/postgres/ -count=1
```
Expected: FAIL/编译错误(方法未实现)。

- [ ] **Step 3: 实现 store.go 的 outbox/events/memories/profiles 方法**

从 `internal/adapters/storage/mysql/store.go` 逐个改写,方言转换规则:
- `?` → `$1..$n`(按参数出现顺序编号)
- `ON DUPLICATE KEY UPDATE x = VALUES(x)` → `ON CONFLICT (主键列) DO UPDATE SET x = EXCLUDED.x`
- `NOW(6)` → `NOW()`

完整实现(追加到 store.go 的 `NewStore` 之后;辅助函数放文件末尾):

```go
func (s *Store) EnqueueOutbox(ctx context.Context, task ports.OutboxTask) error {
	if task.Kind == "" || task.IdempotencyKey == "" || task.ID == "" {
		return errors.New("outbox: id, kind and idempotency key are required")
	}
	if task.Status == "" {
		task.Status = ports.OutboxPending
	}
	if task.AvailableAt.IsZero() {
		task.AvailableAt = time.Now()
	}
	if task.MaxAttempts <= 0 {
		task.MaxAttempts = 5
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now()
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO async_outbox (
			task_id, kind, idempotency_key, payload_json, status, attempts, max_attempts,
			available_at, locked_until, locked_by, last_error, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULL, NULL, NULL, $9, $10)
		ON CONFLICT (kind, idempotency_key) DO UPDATE SET task_id = async_outbox.task_id
	`, task.ID, task.Kind, task.IdempotencyKey, task.Payload, task.Status, task.Attempts, task.MaxAttempts,
		task.AvailableAt, task.CreatedAt, task.UpdatedAt)
	return err
}

func (s *Store) ClaimOutbox(ctx context.Context, workerID string, now time.Time, lease time.Duration, limit int) ([]ports.OutboxTask, error) {
	if workerID == "" {
		return nil, errors.New("outbox: worker id is required")
	}
	if lease <= 0 {
		lease = time.Minute
	}
	if limit <= 0 {
		limit = 1
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT task_id, kind, idempotency_key, payload_json, status, attempts, max_attempts,
		       available_at, locked_until, locked_by, last_error, created_at, updated_at
		FROM async_outbox
		WHERE (status IN ($1, $2) AND available_at <= $3)
		   OR (status = $4 AND (locked_until IS NULL OR locked_until <= $5))
		ORDER BY created_at ASC, task_id ASC
		LIMIT $6
		FOR UPDATE SKIP LOCKED
	`, ports.OutboxPending, ports.OutboxRetry, now, ports.OutboxRunning, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]ports.OutboxTask, 0, limit)
	for rows.Next() {
		var task ports.OutboxTask
		var lockedUntil sql.NullTime
		var lockedBy, lastError sql.NullString
		if err := rows.Scan(&task.ID, &task.Kind, &task.IdempotencyKey, &task.Payload, &task.Status, &task.Attempts, &task.MaxAttempts,
			&task.AvailableAt, &lockedUntil, &lockedBy, &lastError, &task.CreatedAt, &task.UpdatedAt); err != nil {
			return nil, err
		}
		if lockedUntil.Valid {
			task.LockedUntil = lockedUntil.Time
		}
		task.LockedBy = lockedBy.String
		task.LastError = lastError.String
		task.Status = ports.OutboxRunning
		task.Attempts++
		task.LockedBy = workerID
		task.LockedUntil = now.Add(lease)
		task.UpdatedAt = now
		if _, err := tx.ExecContext(ctx, `UPDATE async_outbox SET status = $1, attempts = $2, locked_until = $3, locked_by = $4, updated_at = $5 WHERE task_id = $6`,
			ports.OutboxRunning, task.Attempts, task.LockedUntil, workerID, now, task.ID); err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *Store) CompleteOutbox(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE async_outbox SET status = $1, locked_until = NULL, locked_by = NULL, updated_at = $2 WHERE task_id = $3`, ports.OutboxCompleted, time.Now(), id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errors.New("outbox: task not found")
	}
	return nil
}

func (s *Store) FailOutbox(ctx context.Context, id string, taskErr error, retryAt time.Time) error {
	var attempts, maxAttempts int
	if err := s.db.QueryRowContext(ctx, `SELECT attempts, max_attempts FROM async_outbox WHERE task_id = $1`, id).Scan(&attempts, &maxAttempts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("outbox: task not found")
		}
		return err
	}
	status := ports.OutboxRetry
	if retryAt.IsZero() || (maxAttempts > 0 && attempts >= maxAttempts) {
		status = ports.OutboxDeadLetter
	}
	var retry any
	if status == ports.OutboxRetry {
		retry = retryAt
	}
	_, err := s.db.ExecContext(ctx, `UPDATE async_outbox SET status = $1, available_at = COALESCE($2, available_at), locked_until = NULL, locked_by = NULL, last_error = $3, updated_at = $4 WHERE task_id = $5`,
		status, retry, nullableError(taskErr), time.Now(), id)
	return err
}

func nullableError(err error) any {
	if err == nil {
		return nil
	}
	return err.Error()
}

func (s *Store) ArchiveEvent(ctx context.Context, event conversationdomain.ConversationEvent) error {
	segmentsJSON, err := json.Marshal(event.Segments)
	if err != nil {
		return err
	}
	attachmentsJSON, err := json.Marshal(event.Attachments)
	if err != nil {
		return err
	}

	occurredAt := time.Unix(event.TimestampUnix, 0)
	if event.TimestampUnix == 0 {
		occurredAt = time.Now()
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO messages (
			event_id, group_id, user_id, message_id, reply_to_message_id, kind, text_content,
			segments_json, attachments_json, mentioned_bot, named_bot, is_reply_to_bot,
			occurred_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (event_id) DO UPDATE SET
			text_content = EXCLUDED.text_content,
			segments_json = EXCLUDED.segments_json,
			attachments_json = EXCLUDED.attachments_json,
			mentioned_bot = EXCLUDED.mentioned_bot,
			named_bot = EXCLUDED.named_bot,
			is_reply_to_bot = EXCLUDED.is_reply_to_bot,
			occurred_at = EXCLUDED.occurred_at
	`, event.EventID, event.GroupID, event.UserID, event.MessageID, nullableString(event.ReplyToMessageID), event.Kind,
		event.Text, segmentsJSON, attachmentsJSON, event.MentionedBot, event.NamedBot, event.IsReplyToBot, occurredAt, time.Now())
	return err
}

func (s *Store) RecentEvents(ctx context.Context, groupID int64, limit int) ([]conversationdomain.ConversationEvent, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, group_id, user_id, message_id, reply_to_message_id, kind, text_content,
		       segments_json, attachments_json, mentioned_bot, named_bot, is_reply_to_bot, occurred_at
		FROM messages
		WHERE group_id = $1
		ORDER BY occurred_at DESC
		LIMIT $2
	`, groupID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []conversationdomain.ConversationEvent{}
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	slices.Reverse(events)
	return events, nil
}

func (s *Store) EventsAfter(ctx context.Context, groupID int64, after time.Time, afterEventID string, limit int) ([]conversationdomain.ConversationEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, group_id, user_id, message_id, reply_to_message_id, kind, text_content,
		       segments_json, attachments_json, mentioned_bot, named_bot, is_reply_to_bot, occurred_at
		FROM messages
		WHERE group_id = $1 AND (occurred_at > $2 OR (occurred_at = $3 AND event_id > $4))
		ORDER BY occurred_at ASC, event_id ASC
		LIMIT $5
	`, groupID, after, after, afterEventID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []conversationdomain.ConversationEvent{}
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// scanEvent 从单行扫描 ConversationEvent,RecentEvents/EventsAfter 共用。
func scanEvent(rows *sql.Rows) (conversationdomain.ConversationEvent, error) {
	var (
		event           conversationdomain.ConversationEvent
		replyTo         sql.NullString
		segmentsJSON    []byte
		attachmentsJSON []byte
		occurredAt      time.Time
		kind            string
	)
	if err := rows.Scan(
		&event.EventID,
		&event.GroupID,
		&event.UserID,
		&event.MessageID,
		&replyTo,
		&kind,
		&event.Text,
		&segmentsJSON,
		&attachmentsJSON,
		&event.MentionedBot,
		&event.NamedBot,
		&event.IsReplyToBot,
		&occurredAt,
	); err != nil {
		return conversationdomain.ConversationEvent{}, err
	}
	event.Kind = conversationdomain.EventKind(kind)
	event.ReplyToMessageID = replyTo.String
	event.TimestampUnix = occurredAt.Unix()
	if err := json.Unmarshal(segmentsJSON, &event.Segments); err != nil {
		return conversationdomain.ConversationEvent{}, err
	}
	if err := json.Unmarshal(attachmentsJSON, &event.Attachments); err != nil {
		return conversationdomain.ConversationEvent{}, err
	}
	return event, nil
}

func (s *Store) UpsertMemory(ctx context.Context, record memorydomain.MemoryRecord) error {
	now := time.Now()
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memories (
			memory_id, scope, type, subject, content, source_event_id, descriptor_ref,
			confidence, importance, created_at, expires_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (memory_id) DO UPDATE SET
			scope = EXCLUDED.scope,
			type = EXCLUDED.type,
			subject = EXCLUDED.subject,
			content = EXCLUDED.content,
			source_event_id = EXCLUDED.source_event_id,
			descriptor_ref = EXCLUDED.descriptor_ref,
			confidence = EXCLUDED.confidence,
			importance = EXCLUDED.importance,
			expires_at = EXCLUDED.expires_at,
			updated_at = EXCLUDED.updated_at
	`, record.MemoryID, record.Scope, record.Type, record.Subject, record.Content, record.SourceEventID, record.DescriptorRef,
		record.Confidence, record.Importance, createdAt, nullableTime(record.ExpiresAt), now)
	return err
}

func (s *Store) QueryMemories(ctx context.Context, query ports.MemoryQuery) ([]memorydomain.MemoryRecord, error) {
	base := `
		SELECT memory_id, scope, type, subject, content, source_event_id, descriptor_ref,
		       confidence, importance, created_at, expires_at
		FROM memories
		WHERE 1=1
	`
	args := []any{}
	if query.Scope != "" {
		base += " AND scope = $" + strconv.Itoa(len(args)+1)
		args = append(args, query.Scope)
	}
	if len(query.Types) > 0 {
		phs := make([]string, 0, len(query.Types))
		for _, memoryType := range query.Types {
			args = append(args, memoryType)
			phs = append(phs, "$"+strconv.Itoa(len(args)))
		}
		base += " AND type IN (" + strings.Join(phs, ",") + ")"
	}
	if trimmed := strings.TrimSpace(query.Query); trimmed != "" {
		like := "%" + trimmed + "%"
		base += " AND (subject LIKE $" + strconv.Itoa(len(args)+1) + " OR content LIKE $" + strconv.Itoa(len(args)+2) + ")"
		args = append(args, like, like)
	}
	base += " ORDER BY importance DESC, created_at DESC"
	if query.TopK > 0 {
		base += " LIMIT $" + strconv.Itoa(len(args)+1)
		args = append(args, query.TopK)
	}

	rows, err := s.db.QueryContext(ctx, base, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := []memorydomain.MemoryRecord{}
	for rows.Next() {
		var (
			record    memorydomain.MemoryRecord
			expiresAt sql.NullTime
		)
		if err := rows.Scan(
			&record.MemoryID,
			&record.Scope,
			&record.Type,
			&record.Subject,
			&record.Content,
			&record.SourceEventID,
			&record.DescriptorRef,
			&record.Confidence,
			&record.Importance,
			&record.CreatedAt,
			&expiresAt,
		); err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			record.ExpiresAt = &expiresAt.Time
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) GetMemberProfile(ctx context.Context, groupID, userID int64) (profiledomain.MemberProfile, error) {
	var (
		profile           profiledomain.MemberProfile
		tagsJSON          []byte
		commonPhrasesJSON []byte
		interestsJSON     []byte
		traitsJSON        []byte
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT nickname, message_count, last_spoke_at, active_score, tags_json, common_phrases_json, interests_json, traits_json
		FROM member_profiles
		WHERE group_id = $1 AND user_id = $2
	`, groupID, userID).Scan(
		&profile.Stats.Nickname,
		&profile.Stats.MessageCount,
		&profile.Stats.LastSpokeAt,
		&profile.Stats.ActiveScore,
		&tagsJSON,
		&commonPhrasesJSON,
		&interestsJSON,
		&traitsJSON,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return profiledomain.MemberProfile{Stats: profiledomain.MemberStats{GroupID: groupID, UserID: userID}}, nil
		}
		return profiledomain.MemberProfile{}, err
	}
	profile.Stats.GroupID = groupID
	profile.Stats.UserID = userID
	_ = json.Unmarshal(tagsJSON, &profile.Tags)
	_ = json.Unmarshal(commonPhrasesJSON, &profile.CommonPhrases)
	_ = json.Unmarshal(interestsJSON, &profile.Interests)
	_ = json.Unmarshal(traitsJSON, &profile.Traits)
	return profile, nil
}

func (s *Store) SaveMemberProfile(ctx context.Context, profile profiledomain.MemberProfile) error {
	tagsJSON, _ := json.Marshal(profile.Tags)
	commonPhrasesJSON, _ := json.Marshal(profile.CommonPhrases)
	interestsJSON, _ := json.Marshal(profile.Interests)
	traitsJSON, _ := json.Marshal(profile.Traits)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO member_profiles (
			group_id, user_id, nickname, message_count, last_spoke_at, active_score,
			tags_json, common_phrases_json, interests_json, traits_json, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (group_id, user_id) DO UPDATE SET
			nickname = EXCLUDED.nickname,
			message_count = EXCLUDED.message_count,
			last_spoke_at = EXCLUDED.last_spoke_at,
			active_score = EXCLUDED.active_score,
			tags_json = EXCLUDED.tags_json,
			common_phrases_json = EXCLUDED.common_phrases_json,
			interests_json = EXCLUDED.interests_json,
			traits_json = EXCLUDED.traits_json,
			updated_at = EXCLUDED.updated_at
	`, profile.Stats.GroupID, profile.Stats.UserID, profile.Stats.Nickname, profile.Stats.MessageCount,
		coalesceTime(profile.Stats.LastSpokeAt), profile.Stats.ActiveScore, tagsJSON, commonPhrasesJSON, interestsJSON, traitsJSON, time.Now())
	return err
}

func (s *Store) GetRelationship(ctx context.Context, personaID string, groupID, userID int64) (profiledomain.RelationshipState, error) {
	var state profiledomain.RelationshipState
	err := s.db.QueryRowContext(ctx, `
		SELECT familiarity, affinity, tease_tolerance, grudge_score, last_interact_at
		FROM relationships
		WHERE persona_id = $1 AND group_id = $2 AND user_id = $3
	`, personaID, groupID, userID).Scan(
		&state.Familiarity,
		&state.Affinity,
		&state.TeaseTolerance,
		&state.GrudgeScore,
		&state.LastInteractAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return profiledomain.RelationshipState{
				PersonaID: personaID,
				GroupID:   groupID,
				UserID:    userID,
			}, nil
		}
		return profiledomain.RelationshipState{}, err
	}
	state.PersonaID = personaID
	state.GroupID = groupID
	state.UserID = userID
	return state, nil
}

func (s *Store) SaveRelationship(ctx context.Context, state profiledomain.RelationshipState) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO relationships (
			persona_id, group_id, user_id, familiarity, affinity, tease_tolerance, grudge_score, last_interact_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (persona_id, group_id, user_id) DO UPDATE SET
			familiarity = EXCLUDED.familiarity,
			affinity = EXCLUDED.affinity,
			tease_tolerance = EXCLUDED.tease_tolerance,
			grudge_score = EXCLUDED.grudge_score,
			last_interact_at = EXCLUDED.last_interact_at
	`, state.PersonaID, state.GroupID, state.UserID, state.Familiarity, state.Affinity, state.TeaseTolerance, state.GrudgeScore, coalesceTime(state.LastInteractAt))
	return err
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableTime(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return *value
}

func coalesceTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now()
	}
	return value
}
```

store.go 的 import 块相应更新:

```go
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
)
```

注意:此时恢复 `var _ ports.MemoryStore = (*Store)(nil)` 等已实现接口的断言(MemeStore/ThoughtStore/LearningStateStore 断言继续注释,Task 5 恢复)。

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/adapters/storage/postgres/ -count=1 -v
```
Expected: TestOutboxRoundtrip、TestEventsAndMemories、TestProfiles 全 PASS(PG 未启动时 SKIP)。

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/storage/postgres/
git commit -m "feat(postgres): outbox/events/memories/profiles store methods"
```

### Task 5: memes + working memory + thoughts + watermarks

**Files:**
- Create: `internal/adapters/storage/postgres/runtime_repository.go`
- Modify: `internal/adapters/storage/postgres/store.go`(追加 meme 方法,恢复全部断言)
- Modify: `internal/adapters/storage/postgres/store_test.go`(追加 meme/watermark 测试)

- [ ] **Step 1: 追加测试到 store_test.go**

```go
func TestMemes(t *testing.T) {
	ctx := context.Background()
	db := setupPostgres(t)
	store := NewStore(db)

	memeID := fmt.Sprintf("meme-%d", time.Now().UnixNano())
	if err := store.UpsertMeme(ctx, mediadomain.MemeAsset{
		MemeID:        memeID,
		GroupID:       1,
		SourceEventID: "event-x",
		ObjectKey:     "memes/test.jpg",
		FileExt:       ".jpg",
		ContentHash:   memeID,
		Status:        "approved",
		CreatedAt:     time.Now(),
	}, mediadomain.MemeDescriptor{
		MemeID:     memeID,
		Title:      "离谱图",
		Summary:    "适合接离谱发言",
		Keywords:   []string{"离谱", "吐槽"},
		Confidence: 0.9,
		Reviewed:   true,
		UpdatedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("upsert meme: %v", err)
	}
	memes, err := store.SearchMemes(ctx, ports.MemeQuery{GroupID: 1, Query: "离谱", TopK: 3})
	if err != nil {
		t.Fatalf("search memes: %v", err)
	}
	if len(memes) != 1 {
		t.Fatalf("expected 1 meme, got %d", len(memes))
	}
	asset, descriptor, err := store.GetMeme(ctx, memeID)
	if err != nil {
		t.Fatalf("get meme: %v", err)
	}
	if asset.ObjectKey != "memes/test.jpg" || len(descriptor.Keywords) != 2 {
		t.Fatalf("unexpected meme: %+v %+v", asset, descriptor)
	}
	count, err := store.CountMemesByGroup(ctx, 1)
	if err != nil {
		t.Fatalf("count memes: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected count 1, got %d", count)
	}
	if err := store.MarkMemeSent(ctx, memeID); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	if err := store.DeleteOldestMemes(ctx, 1, 5); err != nil {
		t.Fatalf("delete oldest: %v", err)
	}
	if count, _ := store.CountMemesByGroup(ctx, 1); count != 0 {
		t.Fatalf("expected 0 after delete, got %d", count)
	}
}

func TestWatermarksAndThoughts(t *testing.T) {
	ctx := context.Background()
	db := setupPostgres(t)
	store := NewStore(db)

	wm := memorydomain.LearningWatermark{
		GroupID: 1, Kind: "learning_extract",
		OccurredAt: time.Now(), EventID: "event-1", UpdatedAt: time.Now(),
	}
	if err := store.SaveLearningWatermark(ctx, wm); err != nil {
		t.Fatalf("save watermark: %v", err)
	}
	got, err := store.GetLearningWatermark(ctx, 1, "learning_extract")
	if err != nil {
		t.Fatalf("get watermark: %v", err)
	}
	if got.EventID != "event-1" {
		t.Fatalf("unexpected watermark: %+v", got)
	}

	thought := replydomain.ThoughtRecord{
		ThoughtID: "thought-1", CandidateID: "cand-1", GroupID: 1, EventID: "event-1",
		Interpretation: "test", Uncertainty: 0.2, ChosenAction: "reply", Outcome: "ok",
		CreatedAt: time.Now(),
	}
	if err := store.SaveThought(ctx, thought); err != nil {
		t.Fatalf("save thought: %v", err)
	}

	mem := humandomain.GroupWorkingMemory{GroupID: 1}
	if err := store.SaveWorkingMemory(ctx, mem); err != nil {
		t.Fatalf("save working memory: %v", err)
	}
	loaded, err := store.LoadWorkingMemory(ctx, 1)
	if err != nil {
		t.Fatalf("load working memory: %v", err)
	}
	if loaded.GroupID != 1 {
		t.Fatalf("unexpected working memory: %+v", loaded)
	}
}
```

import 块追加:

```go
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/adapters/storage/postgres/ -count=1
```
Expected: 编译失败(方法未实现)。

- [ ] **Step 3: 实现 meme 方法(追加到 store.go)**

从 mysqlstore 改写,完整代码:

```go
func (s *Store) UpsertMeme(ctx context.Context, asset mediadomain.MemeAsset, descriptor mediadomain.MemeDescriptor) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO meme_assets (
			meme_id, group_id, source_event_id, object_key, file_ext, content_hash, perceptual_hash,
			width, height, animated, status, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (meme_id) DO UPDATE SET
			group_id = EXCLUDED.group_id,
			source_event_id = EXCLUDED.source_event_id,
			object_key = EXCLUDED.object_key,
			file_ext = EXCLUDED.file_ext,
			content_hash = EXCLUDED.content_hash,
			perceptual_hash = EXCLUDED.perceptual_hash,
			width = EXCLUDED.width,
			height = EXCLUDED.height,
			animated = EXCLUDED.animated,
			status = EXCLUDED.status
	`, asset.MemeID, asset.GroupID, asset.SourceEventID, asset.ObjectKey, asset.FileExt, asset.ContentHash, asset.PerceptualHash,
		asset.Width, asset.Height, asset.Animated, asset.Status, coalesceTime(asset.CreatedAt))
	if err != nil {
		return err
	}

	keywordsJSON, _ := json.Marshal(descriptor.Keywords)
	emotionJSON, _ := json.Marshal(descriptor.EmotionTags)
	sceneJSON, _ := json.Marshal(descriptor.SceneTags)
	usageJSON, _ := json.Marshal(descriptor.UsageHints)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO meme_descriptors (
			meme_id, title, summary, keywords_json, emotion_tags_json, scene_tags_json,
			usage_hints_json, language, confidence, reviewed, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (meme_id) DO UPDATE SET
			title = EXCLUDED.title,
			summary = EXCLUDED.summary,
			keywords_json = EXCLUDED.keywords_json,
			emotion_tags_json = EXCLUDED.emotion_tags_json,
			scene_tags_json = EXCLUDED.scene_tags_json,
			usage_hints_json = EXCLUDED.usage_hints_json,
			language = EXCLUDED.language,
			confidence = EXCLUDED.confidence,
			reviewed = EXCLUDED.reviewed,
			updated_at = EXCLUDED.updated_at
	`, descriptor.MemeID, descriptor.Title, descriptor.Summary, keywordsJSON, emotionJSON, sceneJSON,
		usageJSON, descriptor.Language, descriptor.Confidence, descriptor.Reviewed, coalesceTime(descriptor.UpdatedAt))
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) SearchMemes(ctx context.Context, query ports.MemeQuery) ([]mediadomain.MemeSearchResult, error) {
	like := "%" + strings.TrimSpace(query.Query) + "%"
	if like == "%%" {
		like = "%"
	}
	limit := query.TopK
	if limit <= 0 {
		limit = 5
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT a.meme_id, d.title, d.summary, d.keywords_json, d.emotion_tags_json, d.scene_tags_json,
		       d.usage_hints_json, d.language, d.confidence, d.reviewed
		FROM meme_assets a
		JOIN meme_descriptors d ON d.meme_id = a.meme_id
		WHERE (a.group_id = $1 OR a.group_id = 0)
		  AND (d.title LIKE $2 OR d.summary LIKE $3 OR d.keywords_json::text LIKE $4)
		ORDER BY a.send_count DESC, d.updated_at DESC
		LIMIT $5
	`, query.GroupID, like, like, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []mediadomain.MemeSearchResult{}
	for rows.Next() {
		var (
			result       mediadomain.MemeSearchResult
			keywordsJSON []byte
			emotionJSON  []byte
			sceneJSON    []byte
			usageJSON    []byte
			descriptor   mediadomain.MemeDescriptor
		)
		if err := rows.Scan(
			&result.MemeID,
			&descriptor.Title,
			&descriptor.Summary,
			&keywordsJSON,
			&emotionJSON,
			&sceneJSON,
			&usageJSON,
			&descriptor.Language,
			&descriptor.Confidence,
			&descriptor.Reviewed,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(keywordsJSON, &descriptor.Keywords)
		_ = json.Unmarshal(emotionJSON, &descriptor.EmotionTags)
		_ = json.Unmarshal(sceneJSON, &descriptor.SceneTags)
		_ = json.Unmarshal(usageJSON, &descriptor.UsageHints)
		descriptor.MemeID = result.MemeID
		result.Score = descriptor.Confidence
		result.MatchType = "keyword"
		result.MatchedTerms = descriptor.Keywords
		result.Descriptor = descriptor
		results = append(results, result)
	}
	return results, rows.Err()
}

func (s *Store) GetMeme(ctx context.Context, memeID string) (mediadomain.MemeAsset, mediadomain.MemeDescriptor, error) {
	var (
		asset        mediadomain.MemeAsset
		descriptor   mediadomain.MemeDescriptor
		keywordsJSON []byte
		emotionJSON  []byte
		sceneJSON    []byte
		usageJSON    []byte
		lastSentAt   sql.NullTime
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT a.meme_id, a.group_id, a.source_event_id, a.object_key, a.file_ext, a.content_hash,
		       a.perceptual_hash, a.width, a.height, a.animated, a.status, a.created_at, a.last_sent_at,
		       d.title, d.summary, d.keywords_json, d.emotion_tags_json, d.scene_tags_json,
		       d.usage_hints_json, d.language, d.confidence, d.reviewed, d.updated_at
		FROM meme_assets a
		JOIN meme_descriptors d ON d.meme_id = a.meme_id
		WHERE a.meme_id = $1
	`, memeID).Scan(
		&asset.MemeID, &asset.GroupID, &asset.SourceEventID, &asset.ObjectKey, &asset.FileExt, &asset.ContentHash,
		&asset.PerceptualHash, &asset.Width, &asset.Height, &asset.Animated, &asset.Status, &asset.CreatedAt, &lastSentAt,
		&descriptor.Title, &descriptor.Summary, &keywordsJSON, &emotionJSON, &sceneJSON,
		&usageJSON, &descriptor.Language, &descriptor.Confidence, &descriptor.Reviewed, &descriptor.UpdatedAt,
	)
	if err != nil {
		return mediadomain.MemeAsset{}, mediadomain.MemeDescriptor{}, err
	}

	descriptor.MemeID = memeID
	_ = json.Unmarshal(keywordsJSON, &descriptor.Keywords)
	_ = json.Unmarshal(emotionJSON, &descriptor.EmotionTags)
	_ = json.Unmarshal(sceneJSON, &descriptor.SceneTags)
	_ = json.Unmarshal(usageJSON, &descriptor.UsageHints)
	if lastSentAt.Valid {
		asset.LastSentAt = &lastSentAt.Time
	}
	return asset, descriptor, nil
}

func (s *Store) CountMemesByGroup(ctx context.Context, groupID int64) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM meme_assets WHERE group_id = $1`,
		groupID,
	).Scan(&count)
	return count, err
}

func (s *Store) DeleteOldestMemes(ctx context.Context, groupID int64, deleteCount int) error {
	if deleteCount <= 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT meme_id FROM meme_assets WHERE group_id = $1 ORDER BY created_at ASC LIMIT $2`,
		groupID, deleteCount,
	)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			_ = rows.Close()
			return scanErr
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	// PG 占位符按序号生成:IN ($1,$2,...)
	phs := make([]string, 0, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		phs = append(phs, "$"+strconv.Itoa(i+1))
		args[i] = id
	}
	in := "(" + strings.Join(phs, ",") + ")"
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx,
		`DELETE FROM meme_descriptors WHERE meme_id IN `+in, args...); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx,
		`DELETE FROM meme_assets WHERE meme_id IN `+in, args...); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) MarkMemeSent(ctx context.Context, memeID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE meme_assets
		SET send_count = send_count + 1, last_sent_at = $1
		WHERE meme_id = $2
	`, time.Now(), memeID)
	return err
}
```

store.go import 追加 `mediadomain "github.com/phlin/go-agent/internal/domain/media"`。

**SearchMemes 注意**:JSONB 列的 LIKE 匹配需要 `d.keywords_json::text LIKE $4`(JSONB 到 text 的显式转换)。另外 JSONB 的 text 形式带引号和转义,中文关键词在数组里通常能子串命中,行为与 MySQL JSON LIKE 近似——集成测试用"离谱"验证通过即可;若不命中,改成 `d.keywords_json::text LIKE '%' || $4 || '%'` 并直接传原词。

- [ ] **Step 4: 实现 runtime_repository.go**

```go
package postgresstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/phlin/go-agent/internal/domain/memory"
	"github.com/phlin/go-agent/internal/domain/reply"
	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
)

// Working-memory, thought and learning cursor persistence live together
// because they form the runtime projection used to resume background work.
func (s *Store) LoadWorkingMemory(ctx context.Context, groupID int64) (humandomain.GroupWorkingMemory, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT state_json FROM group_working_memory WHERE group_id = $1`, groupID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return humandomain.GroupWorkingMemory{GroupID: groupID}, nil
	}
	if err != nil {
		return humandomain.GroupWorkingMemory{}, err
	}
	var state humandomain.GroupWorkingMemory
	if err := json.Unmarshal(raw, &state); err != nil {
		return humandomain.GroupWorkingMemory{}, err
	}
	return state, nil
}

func (s *Store) SaveWorkingMemory(ctx context.Context, state humandomain.GroupWorkingMemory) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO group_working_memory (group_id, state_json, updated_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (group_id) DO UPDATE SET state_json = EXCLUDED.state_json, updated_at = EXCLUDED.updated_at
	`, state.GroupID, raw, time.Now())
	return err
}

func (s *Store) SaveThought(ctx context.Context, thought reply.ThoughtRecord) error {
	evidence, err := json.Marshal(thought.Evidence)
	if err != nil {
		return err
	}
	if thought.CreatedAt.IsZero() {
		thought.CreatedAt = time.Now()
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO thought_records (
			thought_id, candidate_id, group_id, event_id, interpretation, evidence_json,
			uncertainty, chosen_action, outcome, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (thought_id) DO UPDATE SET
			interpretation = EXCLUDED.interpretation, evidence_json = EXCLUDED.evidence_json,
			uncertainty = EXCLUDED.uncertainty, chosen_action = EXCLUDED.chosen_action, outcome = EXCLUDED.outcome
	`, thought.ThoughtID, thought.CandidateID, thought.GroupID, thought.EventID, thought.Interpretation,
		evidence, thought.Uncertainty, thought.ChosenAction, thought.Outcome, thought.CreatedAt)
	return err
}

func (s *Store) GetLearningWatermark(ctx context.Context, groupID int64, kind string) (memory.LearningWatermark, error) {
	watermark := memory.LearningWatermark{GroupID: groupID, Kind: kind}
	err := s.db.QueryRowContext(ctx, `
		SELECT occurred_at, event_id, updated_at
		FROM learning_watermarks
		WHERE group_id = $1 AND kind = $2
	`, groupID, kind).Scan(&watermark.OccurredAt, &watermark.EventID, &watermark.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return watermark, nil
	}
	return watermark, err
}

func (s *Store) SaveLearningWatermark(ctx context.Context, watermark memory.LearningWatermark) error {
	if watermark.UpdatedAt.IsZero() {
		watermark.UpdatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO learning_watermarks (group_id, kind, occurred_at, event_id, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (group_id, kind) DO UPDATE SET occurred_at = EXCLUDED.occurred_at, event_id = EXCLUDED.event_id, updated_at = EXCLUDED.updated_at
	`, watermark.GroupID, watermark.Kind, watermark.OccurredAt, watermark.EventID, watermark.UpdatedAt)
	return err
}
```

- [ ] **Step 5: 恢复 store.go 全部接口断言并跑测试**

```bash
go test ./internal/adapters/storage/postgres/ -count=1 -v
```
Expected: 全部 PASS。

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/storage/postgres/
git commit -m "feat(postgres): memes/working-memory/thoughts/watermarks methods"
```

### Task 6: 配置层切换(MySQL → Postgres)

**Files:**
- Modify: `internal/config/config.go`(MySQLConfig → PostgresConfig,新增 VectorDim)
- Modify: `internal/config/load.go`(Default、overrideWithEnvSecrets)
- Modify: `internal/config/load_test.go`(env 变量名更新)
- Modify: `configs/config.yaml`

- [ ] **Step 1: config.go 替换配置类型**

删除 `MySQLConfig` 与其 `DSN()` 方法、删除 `QdrantConfig`;`StorageConfig` 改为:

```go
type StorageConfig struct {
	Postgres PostgresConfig `yaml:"postgres"`
	Redis    RedisConfig    `yaml:"redis"`
	MinIO    MinIOConfig    `yaml:"minio"`
}

type PostgresConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Database string `yaml:"database"`
	User     string `yaml:"user"`
	Password string `yaml:"-"`
	SSLMode  string `yaml:"ssl_mode"`
	// VectorDim 是 pgvector 向量列维度,须与 embedding 模型输出一致(ark embedding-large 2048 / lite 1024)。
	// 启动时校验与表结构一致,不一致拒绝启动。
	VectorDim int `yaml:"vector_dim"`
}

func (c PostgresConfig) DSN() string {
	sslMode := c.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=%s",
		url.PathEscape(c.User), url.PathEscape(c.Password),
		net.JoinHostPort(c.Host, strconv.Itoa(c.Port)),
		url.PathEscape(c.Database), sslMode)
}
```

import 块删掉 `mysqlcfg "github.com/go-sql-driver/mysql"`,保留 `net`/`strconv`,追加 `fmt`、`net/url`。

- [ ] **Step 2: load.go 更新 Default 与 env 绑定**

`Default()` 里 Storage 段替换为:

```go
		Storage: StorageConfig{
			Postgres: PostgresConfig{
				Host:      "127.0.0.1",
				Port:      5432,
				Database:  "qqbot",
				User:      "qqbot",
				SSLMode:   "disable",
				VectorDim: 2048,
			},
			Redis:  RedisConfig{Addr: "127.0.0.1:6379"},
			MinIO:  MinIOConfig{Endpoint: "127.0.0.1:9000", Bucket: "qqbot-media", UseSSL: false},
		},
```

`overrideWithEnvSecrets` 里:

```go
	stringOverride("QQBOT_STORAGE_POSTGRES_PASSWORD", &cfg.Storage.Postgres.Password)
```

替换原来的 `QQBOT_STORAGE_MYSQL_PASSWORD` 与 `QQBOT_STORAGE_QDRANT_API_KEY` 两行。

- [ ] **Step 3: load_test.go 更新**

`TestLoadReadsDotEnvSecrets` 里 `.env` 内容改为:

```go
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("QQBOT_QQ_ACCESS_TOKEN=qq-secret\nQQBOT_MAIN_MODEL_API_KEY=model-secret\nQQBOT_STORAGE_POSTGRES_PASSWORD=db-secret\n"), 0o600); err != nil {
```

断言改为:

```go
	if got, want := cfg.Storage.Postgres.Password, "db-secret"; got != want {
		t.Fatalf("postgres password mismatch: got %q want %q", got, want)
	}
```

- [ ] **Step 4: configs/config.yaml 更新 storage 段**

```yaml
storage:
  postgres:
    host: 127.0.0.1
    port: 5432
    database: qqbot
    user: qqbot
    ssl_mode: disable
    # 向量维度,须与 embedding 模型输出维度一致。Doubao embedding-large 为 2048,lite 为 1024。
    vector_dim: 2048
    # 私密项 password 从 .env 的 QQBOT_STORAGE_POSTGRES_PASSWORD 读取。
  redis:
    addr: 127.0.0.1:6379
    db: 0
    # 私密项 password 从 .env 的 QQBOT_STORAGE_REDIS_PASSWORD 读取。
  minio:
    endpoint: 127.0.0.1:9000
    bucket: qqbot-media
    use_ssl: false
    # 私密项 access/secret key 从 .env 的
    # QQBOT_STORAGE_MINIO_ACCESS_KEY / QQBOT_STORAGE_MINIO_SECRET_KEY 读取。
```

同时删除 `configs/config.yaml:245` 附近的 `# 表情包语义向量搜索配置(需配合 storage.qdrant.meme_collection 启用)。` 注释中对 qdrant 的引用,改为 `# 表情包语义向量搜索配置(向量检索由 storage.postgres 提供)。`

- [ ] **Step 5: 编译受影响包**

```bash
go build ./internal/config/ && go test ./internal/config/ -count=1
```
Expected: 编译失败——`graphs.go`/`dependencies.go` 还在引用 `cfg.Storage.MySQL`/`cfg.Storage.Qdrant`。这是预期的(下一步切换装配点)。若只想先验证 config 包本身,可临时 `go vet ./internal/config/`。

- [ ] **Step 6: Commit**

```bash
git add internal/config/ configs/config.yaml
git commit -m "feat(config): postgres config replaces mysql/qdrant"
```

### Task 7: 装配切换(阶段 A 收尾)

**Files:**
- Modify: `internal/runtime/bootstrap/dependencies.go`

- [ ] **Step 1: dependencies.go 切换到 postgresstore**

import 块:`mysqlstore "github.com/phlin/go-agent/internal/adapters/storage/mysql"` 替换为 `postgresstore "github.com/phlin/go-agent/internal/adapters/storage/postgres"`。

`newStoreBundle` 中:

```go
	db, err := postgresstore.Open(ctx, cfg.Storage.Postgres.DSN())
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
```

`applyMySQLMigrations` 改名 `applyPostgresMigrations`,函数体:

```go
func applyPostgresMigrations(ctx context.Context, db *sql.DB) error {
	migrationsDir, err := locateMigrationsDir()
	if err != nil {
		return err
	}
	if err := postgresstore.ApplyMigrations(ctx, db, migrationsDir); err != nil {
		return fmt.Errorf("apply postgres migrations: %w", err)
	}
	return nil
}
```

调用点与 probe 报错文案同步改(`"mysql: %w"` → `"postgres: %w"`),`mysqlPersistentStore` 变量名改 `persistentStore`。

- [ ] **Step 2: graphs.go 临时降级(向量功能暂时 Noop,阶段 B 恢复)**

`buildVectorGraph` 整个函数暂时改为(删除 qdrantstore import):

```go
// buildVectorGraph owns optional vector dependencies and their lifecycle
// registration. The business graph can depend on the returned ports without
// knowing whether vector search is configured or available.
// 阶段 A:向量存储暂降级为 Noop,阶段 B 切到 pgvector 后恢复。
func buildVectorGraph(ctx context.Context, cfg config.Config, factory *modeladapter.Factory, stores *storeBundle) vectorGraph {
	return vectorGraph{meme: ports.NoopVectorMemeStore{}}
}
```

同时 `vectorGraph` 结构体的 `memory *qdrantstore.Store` 字段类型暂改为 `memory ports.VectorMemoryStore`(app.go 已按 `!= nil` 判断,接口零值 nil 判断语义不变)。

- [ ] **Step 3: app.go 变量名清理**

`app.go:81-82` 的 `qdrantVectorStore := vectorGraph.memory` 改名 `vectorMemoryStore`,后续引用同步(仅重命名,逻辑不变)。

- [ ] **Step 4: 全量验证(阶段 A 完成标志)**

```bash
go build ./... && go vet ./... && go test ./... -count=1
```
Expected: 全部通过(mysql/qdrant 旧包还在但已不被引用;postgres 集成测试在 PG 可用时执行)。

- [ ] **Step 5: 手动冒烟:app 连 PG 启动**

```bash
docker compose up -d postgres redis minio
go run ./cmd/qqbotd 2>&1 | head -30
```
Expected: 日志显示迁移应用成功、无 `open postgres`/`apply postgres migrations` 错误(Ctrl-C 退出)。

- [ ] **Step 6: Commit**

```bash
git add internal/runtime/bootstrap/
git commit -m "feat(bootstrap): wire postgres store, stage A complete"
```

---

## 阶段 B:pgvector 替换 Qdrant

### Task 8: 向量表迁移 + vector.go

**Files:**
- Create: `migrations/003_vectors.sql`
- Create: `internal/adapters/storage/postgres/vector.go`

- [ ] **Step 1: 写 003_vectors.sql**

```sql
CREATE TABLE IF NOT EXISTS memory_vectors (
  memory_id VARCHAR(128) PRIMARY KEY,
  content   TEXT NOT NULL,
  embedding vector(2048) NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_memory_vectors_embedding ON memory_vectors USING hnsw (embedding vector_cosine_ops);

CREATE TABLE IF NOT EXISTS meme_vectors (
  meme_id   VARCHAR(128) PRIMARY KEY,
  group_id  BIGINT NOT NULL,
  text      TEXT NOT NULL,
  embedding vector(2048) NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_meme_vectors_group ON meme_vectors (group_id);
CREATE INDEX IF NOT EXISTS idx_meme_vectors_embedding ON meme_vectors USING hnsw (embedding vector_cosine_ops);
```

注意:`vector(2048)` 维度写死。若未来换 embedding 模型,需新迁移重建表(启动校验见 Task 9)。

- [ ] **Step 2: 写 vector_test.go(先写测试)**

`internal/adapters/storage/postgres/vector_test.go`:

```go
package postgresstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/embedding"

	"github.com/phlin/go-agent/internal/core/ports"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

// fakeEmbedder 对相同文本永远返回相同向量,不消耗真实 embedding API。
type fakeEmbedder struct{}

func (fakeEmbedder) EmbedStrings(_ context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	result := make([][]float64, 0, len(texts))
	for _, text := range texts {
		vector := []float64{0, 0, 0, 0}
		for i, r := range []rune(text) {
			vector[i%4] += float64(r%17) / 17
		}
		result = append(result, vector)
	}
	return result, nil
}

func TestVectorMemoryRoundtrip(t *testing.T) {
	ctx := context.Background()
	db := setupPostgres(t)
	store := NewVectorStore(db, fakeEmbedder{}, 4)

	record := memorydomain.MemoryRecord{
		MemoryID: fmt.Sprintf("memory-%d", time.Now().UnixNano()),
		Subject:  "梗",
		Content:  "这个群爱聊旧梗",
	}
	if err := store.StoreMemory(ctx, record); err != nil {
		t.Fatalf("store memory: %v", err)
	}
	// 同 ID 重复写入应覆盖而非报错
	if err := store.StoreMemory(ctx, record); err != nil {
		t.Fatalf("idempotent store: %v", err)
	}

	results, err := store.SearchMemories(ctx, "旧梗", 5, 0.0)
	if err != nil {
		t.Fatalf("search memories: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].MemoryID != record.MemoryID {
		t.Fatalf("unexpected memory id: %s", results[0].MemoryID)
	}

	// threshold=2.0(不可能达到的相似度)应过滤掉全部结果
	none, err := store.SearchMemories(ctx, "旧梗", 5, 2.0)
	if err != nil {
		t.Fatalf("search with high threshold: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("expected 0 results with impossible threshold, got %d", len(none))
	}
}

func TestVectorMemeRoundtrip(t *testing.T) {
	ctx := context.Background()
	db := setupPostgres(t)
	store := NewVectorStore(db, fakeEmbedder{}, 4)

	memeID := fmt.Sprintf("meme-%d", time.Now().UnixNano())
	if err := store.IndexMeme(ctx, memeID, "离谱文学配图", 1); err != nil {
		t.Fatalf("index meme: %v", err)
	}
	// group 过滤:group 2 查不到 group 1 的表情包
	results, err := store.SearchMemes(ctx, 1, "离谱", 5, 0.0)
	if err != nil {
		t.Fatalf("search memes: %v", err)
	}
	if len(results) != 1 || results[0].MemeID != memeID || results[0].MatchType != "vector" {
		t.Fatalf("unexpected results: %+v", results)
	}
	other, err := store.SearchMemes(ctx, 2, "离谱", 5, 0.0)
	if err != nil {
		t.Fatalf("search memes other group: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("expected 0 results for other group, got %d", len(other))
	}

	if err := store.DeleteMeme(ctx, memeID); err != nil {
		t.Fatalf("delete meme: %v", err)
	}
	after, err := store.SearchMemes(ctx, 1, "离谱", 5, 0.0)
	if err != nil {
		t.Fatalf("search after delete: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("expected 0 results after delete, got %d", len(after))
	}
}

var (
	_ ports.VectorMemoryStore = (*VectorStore)(nil)
	_ ports.VectorMemeStore   = (*VectorStore)(nil)
)
```

- [ ] **Step 3: 跑测试确认失败**

```bash
go test ./internal/adapters/storage/postgres/ -count=1
```
Expected: 编译失败(VectorStore 未定义)。

- [ ] **Step 4: 实现 vector.go**

```go
package postgresstore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/pgvector/pgvector-go"

	"github.com/phlin/go-agent/internal/core/ports"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

// VectorStore 基于 pgvector 实现语义向量检索,同一 *sql.DB 上与关系表共用连接池。
// 不依赖 eino-ext:embedder 调用与向量读写都在本包内完成。
type VectorStore struct {
	db       *sql.DB
	embedder embedding.Embedder
	dim      int
}

func NewVectorStore(db *sql.DB, embedder embedding.Embedder, vectorDim int) *VectorStore {
	return &VectorStore{db: db, embedder: embedder, dim: vectorDim}
}

// embed 单条文本;维度与表结构不符时由 PG 侧报错(fail-fast)。
func (s *VectorStore) embed(ctx context.Context, text string) (pgvector.Vector, error) {
	vectors, err := s.embedder.EmbedStrings(ctx, []string{text})
	if err != nil {
		return pgvector.Vector{}, fmt.Errorf("embed text: %w", err)
	}
	if len(vectors) != 1 {
		return pgvector.Vector{}, fmt.Errorf("embed text: expected 1 vector, got %d", len(vectors))
	}
	return toFloat32(vectors[0]), nil
}

func toFloat32(vec []float64) pgvector.Vector {
	out := make([]float32, len(vec))
	for i, v := range vec {
		out[i] = float32(v)
	}
	return pgvector.NewVector(out)
}

// StoreMemory 实现 ports.VectorMemoryStore,以 memory_id 为主键幂等覆盖。
func (s *VectorStore) StoreMemory(ctx context.Context, record memorydomain.MemoryRecord) error {
	vec, err := s.embed(ctx, record.Subject+"\n"+record.Content)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO memory_vectors (memory_id, content, embedding)
		VALUES ($1, $2, $3)
		ON CONFLICT (memory_id) DO UPDATE SET content = EXCLUDED.content, embedding = EXCLUDED.embedding
	`, record.MemoryID, record.Subject+"\n"+record.Content, vec)
	return err
}

// SearchMemories 实现 ports.VectorMemoryStore。
// <=> 是余弦距离(0=相同),相似度 = 1 - distance,低于 threshold 的结果丢弃。
func (s *VectorStore) SearchMemories(ctx context.Context, query string, topK int, threshold float64) ([]memorydomain.MemoryRecord, error) {
	if topK <= 0 {
		topK = 5
	}
	vec, err := s.embed(ctx, query)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT memory_id, content, 1 - (embedding <=> $1) AS similarity
		FROM memory_vectors
		ORDER BY embedding <=> $1
		LIMIT $2
	`, vec, topK)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := []memorydomain.MemoryRecord{}
	for rows.Next() {
		var (
			record     memorydomain.MemoryRecord
			similarity float64
		)
		if err := rows.Scan(&record.MemoryID, &record.Content, &similarity); err != nil {
			return nil, err
		}
		if similarity < threshold {
			continue
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

// IndexMeme 实现 ports.VectorMemeStore,以 meme_id 为主键幂等覆盖。
func (s *VectorStore) IndexMeme(ctx context.Context, memeID string, text string, groupID int64) error {
	vec, err := s.embed(ctx, text)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO meme_vectors (meme_id, group_id, text, embedding)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (meme_id) DO UPDATE SET group_id = EXCLUDED.group_id, text = EXCLUDED.text, embedding = EXCLUDED.embedding
	`, memeID, groupID, text, vec)
	return err
}

// SearchMemes 实现 ports.VectorMemeStore。
// group 过滤下推到 SQL(WHERE group_id),topK 不被其他群的数据稀释。
// Descriptor 字段为零值,由上层 MemeService 回查关系表补全。
func (s *VectorStore) SearchMemes(ctx context.Context, groupID int64, queryText string, topK int, threshold float64) ([]mediadomain.MemeSearchResult, error) {
	if topK <= 0 {
		topK = 5
	}
	vec, err := s.embed(ctx, queryText)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT meme_id, 1 - (embedding <=> $1) AS similarity
		FROM meme_vectors
		WHERE group_id = $2
		ORDER BY embedding <=> $1
		LIMIT $3
	`, vec, groupID, topK)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []mediadomain.MemeSearchResult{}
	for rows.Next() {
		var (
			result     mediadomain.MemeSearchResult
			similarity float64
		)
		if err := rows.Scan(&result.MemeID, &similarity); err != nil {
			return nil, err
		}
		if similarity < threshold {
			continue
		}
		result.Score = similarity
		result.MatchType = "vector"
		results = append(results, result)
	}
	return results, rows.Err()
}

// DeleteMeme 实现 ports.VectorMemeStore。
func (s *VectorStore) DeleteMeme(ctx context.Context, memeID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM meme_vectors WHERE meme_id = $1`, memeID)
	return err
}
```

注意:**向量作为参数传入 SQL 时直接用 `pgvector.Vector`**(根包实现了 `driver.Valuer`,经 database/sql 会序列化为 text 格式,pgx stdlib 兼容);不需要 `pgxvec.RegisterTypes`(那是二进制协议优化,非必需)。dim 字段当前未使用——保留它作为文档性字段,Task 9 启动校验用配置值即可。若 `go vet` 报 unused field 警告(lint 工具),把 dim 用在构造时校验:`if vectorDim <= 0 { vectorDim = 2048 }` 后存字段即可。

- [ ] **Step 5: 跑测试确认通过**

```bash
go test ./internal/adapters/storage/postgres/ -count=1 -v -run 'Vector'
```
Expected: TestVectorMemoryRoundtrip、TestVectorMemeRoundtrip PASS。

- [ ] **Step 6: Commit**

```bash
git add migrations/003_vectors.sql internal/adapters/storage/postgres/
git commit -m "feat(postgres): pgvector memory/meme vector store"
```

### Task 9: 向量装配切换(阶段 B 收尾)

**Files:**
- Modify: `internal/runtime/bootstrap/graphs.go`
- Modify: `internal/runtime/bootstrap/dependencies.go`(向量相关装配挪入或引用)

- [ ] **Step 1: graphs.go 切换到 pgvector**

```go
package bootstrap

import (
	"context"
	"log/slog"

	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
	postgresstore "github.com/phlin/go-agent/internal/adapters/storage/postgres"
	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/core/ports"
)

type vectorGraph struct {
	memory ports.VectorMemoryStore
	meme   ports.VectorMemeStore
}

// buildVectorGraph owns optional vector dependencies and their lifecycle
// registration. The business graph can depend on the returned ports without
// knowing whether vector search is configured or available.
func buildVectorGraph(ctx context.Context, cfg config.Config, factory *modeladapter.Factory, stores *storeBundle) vectorGraph {
	graph := vectorGraph{meme: ports.NoopVectorMemeStore{}}
	if cfg.Storage.Postgres.VectorDim <= 0 {
		return graph
	}
	embedder, err := factory.EmbeddingModel(ctx)
	if err != nil {
		slog.Warn("app: embedding model unavailable, skipping vector store init", "err", err)
		return graph
	}
	// 向量库与关系库共用 *sql.DB:同一 PG 实例、同一连接池。
	graph.memory = postgresstore.NewVectorStore(stores.db, embedder, cfg.Storage.Postgres.VectorDim)
	graph.meme = graph.memory
	return graph
}
```

这需要 `storeBundle` 暴露 `db`。`dependencies.go` 的 `storeBundle` 结构体加字段 `db *sql.DB`,`newStoreBundle` 里 `bundle.db = db`。

- [ ] **Step 2: 删除 meme_collection 语义的遗留引用**

`graphs.go` 原 `MemeCollection`/`SemanticTopK` 拼装逻辑整个删除(向量 topK 由调用方通过 `SearchMemories` 的 topK 参数传入,服务层已有 `cfg.Meme.SemanticTopK` 消费,检索时传入;`NewVectorStore` 不再持有 topK)。

- [ ] **Step 3: app.go 清理(若有编译错误)**

`app.go` 中 `vectorMemoryStore`(Task 7 改名后)类型已是接口,`!= nil` 判断对接口值仍然有效;`memOpts = append(memOpts, memsvc.WithVectorStore(vectorMemoryStore))` 不变。若 `vectorGraph.memory` 为 nil 接口,判断走 Noop 分支,行为与 Qdrant 时代一致。

- [ ] **Step 4: 全量验证(阶段 B 完成标志)**

```bash
go build ./... && go vet ./... && go test ./... -count=1
```
Expected: 全部通过。

- [ ] **Step 5: 手动冒烟**

```bash
docker compose up -d postgres redis minio
go run ./cmd/qqbotd 2>&1 | head -30
```
Expected: 启动无错,日志无 `vector`/`embedding` 相关 fatal。

- [ ] **Step 6: Commit**

```bash
git add internal/runtime/bootstrap/
git commit -m "feat(bootstrap): wire pgvector store, stage B complete"
```

---

## 阶段 C:删除旧栈

### Task 10: 删代码 + 删依赖

**Files:**
- Delete: `internal/adapters/storage/mysql/`(整目录)
- Delete: `internal/adapters/storage/qdrant/`(整目录)
- Modify: `go.mod`/`go.sum`

- [ ] **Step 1: 删除旧 adapter 目录**

```bash
rm -rf internal/adapters/storage/mysql internal/adapters/storage/qdrant
```

- [ ] **Step 2: 清理 go.mod**

```bash
go mod tidy
```
Expected: `go-sql-driver/mysql`、`qdrant/go-client`、`eino-ext` 的 qdrant/embedding 组件依赖消失(`eino-ext/components/model/*` 保留)。

- [ ] **Step 3: 编译验证**

```bash
go build ./... && go vet ./...
```
Expected: 成功。若报错,说明有遗漏的引用(如 `factory.go` 里 `embedding/ark`、`embedding/openai` 是 eino-ext 的——**这两个必须保留**,它们是 embedder 实现本身,与被删的 qdrant retriever/indexer 不同;tidy 会自动判断)。

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "refactor(storage): remove mysql and qdrant adapters"
```

### Task 11: 删基础设施 + 文档更新

**Files:**
- Modify: `docker-compose.yml`(删 mysql/qdrant 服务)
- Modify: `.env.example`、`.env`(改 PG 密码变量名)
- Modify: `README.md`
- Modify: 注释提及旧栈的服务层文件

- [ ] **Step 1: docker-compose.yml 删除 mysql 和 qdrant 服务块**

整个 `mysql:` 服务块(约 31 行)和 `qdrant:` 服务块(约 18 行)删除。保留 postgres/redis/minio/napcat。

- [ ] **Step 2: .env / .env.example 更新**

`.env.example` 基础设施段替换为:

```bash
# 基础设施密钥。示例值与 docker-compose.yml 的本地默认值保持一致。
QQBOT_STORAGE_POSTGRES_PASSWORD=qqbotpass
QQBOT_STORAGE_REDIS_PASSWORD=
QQBOT_STORAGE_MINIO_ACCESS_KEY=minioadmin
QQBOT_STORAGE_MINIO_SECRET_KEY=minioadmin123
```

`.env` 同步改(实际密钥值由用户自己的 .env 保留,只改变量名)。

- [ ] **Step 3: README.md 更新**

- L13: `Qdrant 向量检索 + MySQL 结构化存储` → `PostgreSQL + pgvector 向量检索与结构化存储`
- L81: `启动 MySQL、Redis、Qdrant、MinIO、NapCat 五个服务。` → `启动 PostgreSQL、Redis、MinIO、NapCat 四个服务。`
- L169 环境变量表:`QQBOT_STORAGE_MYSQL_PASSWORD | MySQL 密码` → `QQBOT_STORAGE_POSTGRES_PASSWORD | PostgreSQL 密码`
- L193: `storage | MySQL、Redis、Qdrant、MinIO 连接信息` → `storage | PostgreSQL、Redis、MinIO 连接信息`
- L215: `migrations/ | MySQL 初始化脚本` → `migrations/ | PostgreSQL 迁移脚本`
- L250: `MySQL / Redis / Qdrant / MinIO` → `PostgreSQL / Redis / MinIO`
- L261-266 数据库迁移段替换为:

```markdown
## 数据库迁移

PostgreSQL 迁移脚本在 [`migrations/`](migrations/),由应用启动时自动执行(含 pgvector 扩展与向量表),无需手动导入。
```

- L55: `Group Actor 写入 Event Log 和 MySQL 消息归档` → `Group Actor 写入 Event Log 和 PostgreSQL 消息归档`

- [ ] **Step 4: 服务层注释清理(仅注释,无逻辑变更)**

- `internal/services/context/service.go:216-219` 附近:`MySQL 关键词检索` → `结构化关键词检索`,`Qdrant 语义检索` → `语义向量检索`;局部变量 `mysqlRecords`/`qdrantRecords`/`mysqlTopK` 相应改名 `structuredRecords`/`vectorRecords`/`structuredTopK`(纯重命名)
- `internal/services/memory/service.go:31,99,104,120,128`:`Qdrant` → `向量库`,`MySQL` → `主存储`,job 名 `qdrant_store_memory` → `vector_store_memory`
- `internal/services/meme/service.go:168,186`:`回查 MySQL` → `回查关系表`
- `internal/core/ports/ports.go:130,138,150,151,161` 注释:`qdrantstore.Store` → `postgresstore.VectorStore`,`Qdrant 未配置` → `向量检索未配置`,`回查 MySQL` → `回查关系表`

注意:`memory/service.go` 里 outbox kind 字符串 `"memory_vector_index"`/`"meme_vector_index"` 不改——它们是任务类型名,与存储实现无关(历史任务名保留,幂等 key 不受影响;反正数据从零开始)。

- [ ] **Step 5: 停旧容器 + 删旧数据卷**

```bash
docker compose stop mysql qdrant
docker compose rm -f mysql qdrant
rm -rf data/mysql data/qdrant
```

**执行前需向用户确认**——这是不可逆删除(191MB MySQL + 35MB Qdrant 数据)。用户已确认从零开始,但删除动作仍要在执行时口头再确认一次。

- [ ] **Step 6: compose 配置验证**

```bash
docker compose config
```
Expected: 无错误,服务列表只有 postgres/redis/minio/napcat。

- [ ] **Step 7: 全量最终验证**

```bash
go build ./... && go vet ./... && go test ./... -count=1 && go test -race ./internal/adapters/storage/postgres/ -count=1
```
Expected: 全部通过。

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "chore(infra): remove mysql/qdrant services and docs, migration complete"
```

---

## 验收清单(对照 spec)

- [ ] `docker compose config` 只含 postgres/redis/minio/napcat
- [ ] `go run ./cmd/qqbotd` 连 PG 启动,迁移自动应用(含 vector 扩展)
- [ ] 全量测试通过,postgres 集成测试在本地 PG 可达时执行
- [ ] `go.mod` 无 go-sql-driver/mysql、qdrant/go-client、eino-ext qdrant 组件;保留 eino-ext model/embedding 组件
- [ ] `grep -ri "mysql\|qdrant" --include="*.go" internal/ cmd/` 无结果(注释也已清理)
- [ ] meme 向量检索按 group_id 在 SQL 层过滤(topK 不被稀释)
- [ ] threshold 语义:相似度 = 1 - 余弦距离,低于 threshold 丢弃
