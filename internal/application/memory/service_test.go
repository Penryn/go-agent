package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/application/ports"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

type recordingMemoryStore struct {
	ports.MemoryStore
	record memorydomain.MemoryRecord
	err    error
}

func (s *recordingMemoryStore) UpsertMemory(_ context.Context, record memorydomain.MemoryRecord) error {
	s.record = record
	return s.err
}

func TestExplicitMemoryDerivesScopeEvidenceAndStableID(t *testing.T) {
	store := &recordingMemoryStore{}
	service := New(store)
	input := ExplicitMemoryInput{GroupID: 1, UserID: 7, Content: "喜欢咖啡", SourceEventID: "event"}
	first, err := service.RememberExplicit(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.RememberExplicit(context.Background(), input)
	if err != nil || first.MemoryID != second.MemoryID || first.Scope != "group:1:user:7" || first.Subject != "7" || first.Origin != "user_explicit" || first.SourceEventIDs[0] != "event" {
		t.Fatalf("explicit memory authority changed: first=%+v second=%+v err=%v", first, second, err)
	}
	input.SourceEventID = ""
	if _, err := service.RememberExplicit(context.Background(), input); err == nil {
		t.Fatal("memory without evidence was accepted")
	}
	input.SourceEventID, input.UserID = "event", 0
	if _, err := service.RememberExplicit(context.Background(), input); err == nil {
		t.Fatal("memory without an author was accepted")
	}
}

func TestMarkIntentPreservesStructuredPreferenceAndEvidenceTime(t *testing.T) {
	store := &recordingMemoryStore{}
	observed := time.Now().Add(-time.Hour)
	until := time.Now().Add(time.Minute)
	record, err := New(store, WithTypeTTL(nil, "24h")).MarkIntent(context.Background(), WriteIntent{
		Scope: "group:1", Subject: "7", MemoryType: "social", Content: "不要戳我", SourceEventID: "event",
		Predicate: memorydomain.PredicateAllowPoke, NormalizedValue: "false", Qualifier: "default",
		ValidUntil: &until, ObservedAt: observed,
	})
	if err != nil || record.Predicate != memorydomain.PredicateAllowPoke || record.NormalizedValue != "false" || record.Qualifier != "default" || !record.CreatedAt.Equal(observed) || record.ExpiresAt == nil || !record.ExpiresAt.Equal(until) {
		t.Fatalf("structured preference was lost: %+v err=%v", record, err)
	}
	if record.ExpiresAt == &until {
		t.Fatal("record retained a mutable caller-owned expiry pointer")
	}
}

func TestMemoryWriteFailureIsReturned(t *testing.T) {
	store := &recordingMemoryStore{err: errors.New("storage unavailable")}
	_, err := New(store).RememberExplicit(context.Background(), ExplicitMemoryInput{GroupID: 1, UserID: 7, Content: "记住", SourceEventID: "event"})
	if !errors.Is(err, store.err) {
		t.Fatalf("memory write failed silently: %v", err)
	}
}
