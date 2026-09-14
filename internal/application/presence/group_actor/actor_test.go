package group_actor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/application/presence/ingress"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
)

type workingMemoryStoreStub struct {
	memory  presencedomain.GroupWorkingMemory
	saveErr error
}

func (s *workingMemoryStoreStub) LoadWorkingMemory(context.Context, int64) (presencedomain.GroupWorkingMemory, error) {
	return cloneMemory(s.memory), nil
}

func (s *workingMemoryStoreStub) SaveWorkingMemory(_ context.Context, memory presencedomain.GroupWorkingMemory) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.memory = cloneMemory(memory)
	return nil
}

func TestObserveDoesNotAdvanceActorWhenPersistenceFails(t *testing.T) {
	store := &workingMemoryStoreStub{memory: presencedomain.GroupWorkingMemory{GroupID: 1}, saveErr: errors.New("store unavailable")}
	manager := NewManager(ingress.NewMemoryEventLog(), WithStateStore(store))
	defer manager.Close()
	record := testEventRecord("event-1")

	if _, err := manager.Observe(context.Background(), record); err == nil {
		t.Fatal("expected persistence error")
	}
	failed, err := manager.Snapshot(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Version != 0 || len(failed.RecentTail) != 0 {
		t.Fatalf("failed persistence advanced live actor: %+v", failed)
	}

	store.saveErr = nil
	retried, err := manager.Observe(context.Background(), record)
	if err != nil {
		t.Fatalf("retry observation: %v", err)
	}
	if retried.Version != 1 || len(retried.RecentTail) != 1 {
		t.Fatalf("retry did not advance actor exactly once: %+v", retried)
	}
}

func TestUpdateRollsBackWhenPersistenceFails(t *testing.T) {
	store := &workingMemoryStoreStub{memory: presencedomain.GroupWorkingMemory{GroupID: 1, ActiveTopic: "old"}, saveErr: errors.New("store unavailable")}
	manager := NewManager(ingress.NewMemoryEventLog(), WithStateStore(store))
	defer manager.Close()

	err := manager.Update(context.Background(), 1, func(memory *presencedomain.GroupWorkingMemory) error {
		memory.ActiveTopic = "new"
		return nil
	})
	if err == nil {
		t.Fatal("expected persistence error")
	}
	snapshot, snapshotErr := manager.Snapshot(context.Background(), 1)
	if snapshotErr != nil {
		t.Fatal(snapshotErr)
	}
	if snapshot.ActiveTopic != "old" {
		t.Fatalf("failed update leaked into live actor: %+v", snapshot)
	}
}

func TestPromptSessionRejectsStaleProjection(t *testing.T) {
	store := &workingMemoryStoreStub{memory: presencedomain.GroupWorkingMemory{GroupID: 1}}
	manager := NewManager(ingress.NewMemoryEventLog(), WithStateStore(store))
	defer manager.Close()

	first, err := manager.Observe(context.Background(), testEventRecord("event-1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Observe(context.Background(), testEventRecord("event-2")); err != nil {
		t.Fatal(err)
	}
	stale := conversationdomain.PromptSession{Version: "v1", Messages: []conversationdomain.PromptMessage{{Role: "assistant", Content: "stale"}}}
	err = manager.UpdatePromptSession(context.Background(), 1, first.Version, first.PromptSession.Revision, stale)
	if !errors.Is(err, ErrStalePromptSession) {
		t.Fatalf("expected stale prompt session error, got %v", err)
	}

	current, err := manager.Snapshot(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdatePromptSession(context.Background(), 1, current.Version, current.PromptSession.Revision, stale); err != nil {
		t.Fatalf("save current prompt session: %v", err)
	}
	saved, err := manager.Snapshot(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if saved.PromptSession.Revision != 1 || len(saved.PromptSession.Messages) != 1 || saved.PromptSession.Messages[0].Content != "stale" {
		t.Fatalf("current prompt session was not committed: %+v", saved.PromptSession)
	}
}

func TestPromptSessionDoesNotMutateActorWhenPersistenceFails(t *testing.T) {
	store := &workingMemoryStoreStub{memory: presencedomain.GroupWorkingMemory{GroupID: 1}, saveErr: errors.New("store unavailable")}
	manager := NewManager(ingress.NewMemoryEventLog(), WithStateStore(store))
	defer manager.Close()
	session := conversationdomain.PromptSession{Version: "v1", Messages: []conversationdomain.PromptMessage{{Role: "assistant", Content: "unsaved"}}}

	if err := manager.UpdatePromptSession(context.Background(), 1, 0, 0, session); err == nil {
		t.Fatal("expected persistence error")
	}
	snapshot, err := manager.Snapshot(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.PromptSession.Revision != 0 || len(snapshot.PromptSession.Messages) != 0 {
		t.Fatalf("failed prompt session leaked into live actor: %+v", snapshot.PromptSession)
	}
}

func testEventRecord(eventID string) presencedomain.EventRecord {
	now := time.Now()
	return presencedomain.EventRecord{
		EventID:   eventID,
		GroupID:   1,
		UserID:    2,
		Origin:    presencedomain.OriginInbound,
		Timestamp: now,
		Event: conversationdomain.ConversationEvent{
			EventID: eventID, GroupID: 1, UserID: 2, Kind: conversationdomain.EventMessage, Text: eventID, TimestampUnix: now.Unix(),
		},
	}
}
