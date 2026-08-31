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
