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
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
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

	if err := ApplySchema(ctx, db, filepath.Join("..", "..", "..", "..", "schema", "schema.sql")); err != nil {
		t.Fatalf("apply schema: %v", err)
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

func TestAtomicMemeProjection(t *testing.T) {
	ctx := context.Background()
	db := setupPostgres(t)
	store := NewStore(db)
	memeID := fmt.Sprintf("meme-atomic-%d", time.Now().UnixNano())
	revision := time.Now().UnixNano()
	if err := store.UpsertMemeAndEnqueueVector(ctx, mediadomain.MemeAsset{
		MemeID: memeID, GroupID: 1, ObjectKey: "atomic.webp", Status: "approved", Revision: revision,
	}, mediadomain.MemeDescriptor{MemeID: memeID, Summary: "atomic"}, ports.OutboxTask{
		ID: "task-" + memeID, Kind: "meme_vector_index", IdempotencyKey: memeID,
		Payload: []byte(`{"meme_id":"` + memeID + `"}`),
	}); err != nil {
		t.Fatalf("atomic upsert: %v", err)
	}
	asset, _, err := store.GetMeme(ctx, memeID)
	if err != nil {
		t.Fatalf("get atomic meme: %v", err)
	}
	if asset.Revision != revision {
		t.Fatalf("revision = %d, want %d", asset.Revision, revision)
	}
	claimed, err := store.ClaimOutbox(ctx, "atomic-test-worker", time.Now(), time.Minute, 10)
	if err != nil {
		t.Fatalf("claim atomic task: %v", err)
	}
	if len(claimed) != 1 || claimed[0].Kind != "meme_vector_index" {
		t.Fatalf("unexpected atomic tasks: %+v", claimed)
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
	// RecentThoughts:保存后可按群读回(新到旧)
	thoughts, err := store.RecentThoughts(ctx, 1, 5)
	if err != nil {
		t.Fatalf("recent thoughts: %v", err)
	}
	if len(thoughts) != 1 || thoughts[0].ThoughtID != "thought-1" {
		t.Fatalf("unexpected thoughts: %+v", thoughts)
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

func TestRuntimeStates(t *testing.T) {
	ctx := context.Background()
	db := setupPostgres(t)
	store := NewStateStore(db)

	// runtime state:保存后可读回
	rs := policydomain.RuntimeState{GroupID: 1, State: policydomain.StateObserving}
	if err := store.SaveRuntimeState(ctx, rs); err != nil {
		t.Fatalf("save runtime state: %v", err)
	}
	got, err := store.GetRuntimeState(ctx, 1)
	if err != nil {
		t.Fatalf("get runtime state: %v", err)
	}
	if got.State != policydomain.StateObserving {
		t.Fatalf("unexpected runtime state: %+v", got)
	}

	// persona state:未保存时返回默认值(mood=steady)
	pDefault, err := store.GetPersonaState(ctx, "main", 1)
	if err != nil {
		t.Fatalf("get default persona state: %v", err)
	}
	if pDefault.Mood != "steady" || pDefault.PersonaID != "main" {
		t.Fatalf("unexpected default persona state: %+v", pDefault)
	}

	// persona state:保存后可读回
	ps := personadomain.PersonaState{
		PersonaID: "main", GroupID: 1,
		Mood: "excited", Energy: "high",
		ExpiresAt: time.Now().Add(2 * time.Hour),
	}
	if err := store.SavePersonaState(ctx, ps); err != nil {
		t.Fatalf("save persona state: %v", err)
	}
	gotPs, err := store.GetPersonaState(ctx, "main", 1)
	if err != nil {
		t.Fatalf("get persona state: %v", err)
	}
	if gotPs.Mood != "excited" {
		t.Fatalf("unexpected persona state: %+v", gotPs)
	}

	// TTL 语义:expires_at 已过期的状态视为不存在,返回默认值
	expired := personadomain.PersonaState{
		PersonaID: "main", GroupID: 2,
		Mood: "angry", Energy: "low",
		ExpiresAt: time.Now().Add(-time.Minute),
	}
	if err := store.SavePersonaState(ctx, expired); err != nil {
		t.Fatalf("save expired persona state: %v", err)
	}
	gotExpired, err := store.GetPersonaState(ctx, "main", 2)
	if err != nil {
		t.Fatalf("get expired persona state: %v", err)
	}
	if gotExpired.Mood != "steady" {
		t.Fatalf("expired state should fall back to default, got %+v", gotExpired)
	}
}

func TestMemoryForgetting(t *testing.T) {
	ctx := context.Background()
	db := setupPostgres(t)
	store := NewStore(db)

	fresh := memorydomain.MemoryRecord{
		MemoryID: fmt.Sprintf("fresh-%d", time.Now().UnixNano()),
		Scope:    "group:1", Type: "preference",
		Subject: "新梗", Content: "最近在聊的新梗",
		Importance: 0.5, CreatedAt: time.Now().Add(-time.Hour),
	}
	stale := memorydomain.MemoryRecord{
		MemoryID: fmt.Sprintf("stale-%d", time.Now().UnixNano()),
		Scope:    "group:1", Type: "preference",
		Subject: "旧事", Content: "很久以前的旧事",
		Importance: 0.9, CreatedAt: time.Now().AddDate(0, 0, -30),
	}
	expired := memorydomain.MemoryRecord{
		MemoryID: fmt.Sprintf("exp-%d", time.Now().UnixNano()),
		Scope:    "group:1", Type: "preference",
		Subject: "过期", Content: "已过期的记忆",
		Importance: 0.95, CreatedAt: time.Now().Add(-time.Hour),
		ExpiresAt: &[]time.Time{time.Now().Add(-time.Minute)}[0],
	}
	for _, r := range []memorydomain.MemoryRecord{fresh, stale, expired} {
		if err := store.UpsertMemory(ctx, r); err != nil {
			t.Fatalf("upsert %s: %v", r.MemoryID, err)
		}
	}
	records, err := store.QueryMemories(ctx, ports.MemoryQuery{Scope: "group:1", TopK: 10})
	if err != nil {
		t.Fatalf("query memories: %v", err)
	}
	// 过期的不返回
	for _, r := range records {
		if r.MemoryID == expired.MemoryID {
			t.Fatalf("expired memory should not be recalled")
		}
	}
	// 半衰贴现:importance 0.5 的 1 小时新记忆应排在 importance 0.9 的 30 天旧记忆前面
	if len(records) >= 2 {
		if records[0].MemoryID != fresh.MemoryID {
			t.Fatalf("expected fresh memory ranked first (half-life discount), got %s", records[0].MemoryID)
		}
	}
}
