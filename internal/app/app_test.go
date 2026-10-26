package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	postgresstore "github.com/phlin/go-agent/internal/adapters/storage/postgres"
	"github.com/phlin/go-agent/internal/config"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	"github.com/phlin/go-agent/internal/testsupport"
)

func TestAppUsesUnifiedMemorySnapshotAndCloses(t *testing.T) {
	db := testsupport.NewDB(t)
	cfg := config.Default()
	cfg.Storage.Postgres.Password = "qqbotpass"
	if err := db.QueryRow("SELECT current_database()").Scan(&cfg.Storage.Postgres.Database); err != nil {
		t.Fatal(err)
	}
	cfg.QQ.SelfID = 123456
	cfg.Storage.Postgres.VectorDim = 0
	cfg.Storage.Postgres.ObservabilityRetentionDays = 0
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := postgresstore.NewStore(db)
	if err := store.UpsertMemory(ctx, memorydomain.MemoryRecord{
		MemoryID: "preference", Scope: "group:100", Subject: "200", Type: "social", Content: "不要戳我",
		Predicate: memorydomain.PredicateAllowPoke, NormalizedValue: "false", Qualifier: "default",
	}); err != nil {
		t.Fatal(err)
	}
	application, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	health := httptest.NewRecorder()
	application.handleHealth(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health failed: %s", health.Body.String())
	}
	payload := []byte(`{"post_type":"message","message_type":"group","self_id":123456,"group_id":100,"user_id":200,"message_id":"smoke","message":[{"type":"at","data":{"qq":"123456"}},{"type":"text","data":{"text":"你在吗"}}]}`)
	outcome, err := application.ProcessRawEvent(ctx, payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Snapshot.MemoryConstraints) != 1 || outcome.Snapshot.MemoryConstraints[0].Value != "false" {
		t.Fatalf("app wired an inconsistent snapshot: %+v", outcome.Snapshot)
	}
	if err := application.Close(); err != nil {
		t.Fatal(err)
	}
	if err := application.Close(); err != nil {
		t.Fatalf("repeat close failed: %v", err)
	}
}
