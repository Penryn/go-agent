package mysqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
)

func TestStoreIntegration(t *testing.T) {
	ctx := context.Background()
	db := setupMySQL(t)
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

	events, err := store.RecentEvents(ctx, 1, 5)
	if err != nil {
		t.Fatalf("recent events: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("expected recent events")
	}

	record := memorydomain.MemoryRecord{
		MemoryID:      fmt.Sprintf("mem-preference-%d-0001", time.Now().UnixNano()),
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
	if len(records) == 0 {
		t.Fatalf("expected memory query results")
	}

	profile := profiledomain.MemberProfile{
		Stats: profiledomain.MemberStats{
			GroupID:      1,
			UserID:       2,
			Nickname:     "alice",
			MessageCount: 7,
			LastSpokeAt:  time.Now(),
			ActiveScore:  0.8,
		},
		Tags: []string{"老群友"},
	}
	if err := store.SaveMemberProfile(ctx, profile); err != nil {
		t.Fatalf("save member profile: %v", err)
	}
	savedProfile, err := store.GetMemberProfile(ctx, 1, 2)
	if err != nil {
		t.Fatalf("get member profile: %v", err)
	}
	if savedProfile.Stats.Nickname != "alice" {
		t.Fatalf("unexpected nickname: %s", savedProfile.Stats.Nickname)
	}

	memeID := fmt.Sprintf("meme-%d", time.Now().UnixNano())
	if err := store.UpsertMeme(ctx, mediadomain.MemeAsset{
		MemeID:        memeID,
		GroupID:       1,
		SourceEventID: event.EventID,
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
	if len(memes) == 0 {
		t.Fatalf("expected meme search results")
	}
}

func setupMySQL(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()

	adminDB, err := Open(ctx, "root:rootpass@tcp(127.0.0.1:3306)/?parseTime=true&charset=utf8mb4")
	if err != nil {
		t.Skipf("mysql unavailable: %v", err)
	}
	defer adminDB.Close()

	if _, err := adminDB.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS qqbot_test CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		t.Fatalf("create test database: %v", err)
	}

	db, err := Open(ctx, "root:rootpass@tcp(127.0.0.1:3306)/qqbot_test?parseTime=true&charset=utf8mb4")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}

	if err := ApplyMigrations(ctx, db, filepath.Join("..", "..", "..", "..", "migrations")); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	return db
}
