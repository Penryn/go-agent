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

func TestReduceBoundsAndResolvesOpenLoops(t *testing.T) {
	memory := presencedomain.GroupWorkingMemory{GroupID: 1}
	base := time.Unix(100, 0)
	for i, text := range []string{"第一个？", "第二个？", "第三个？", "第四个？"} {
		record := testEventRecord(text)
		record.Event.Text = text
		record.Timestamp = base.Add(time.Duration(i) * time.Second)
		memory = reduce(memory, record, 32)
	}
	if len(memory.OpenLoops) != maxOpenLoops || memory.OpenLoops[0] != "第二个？" {
		t.Fatalf("open loops were not bounded to newest entries: %#v", memory.OpenLoops)
	}

	outbound := testEventRecord("out")
	outbound.Origin = presencedomain.OriginOutbound
	outbound.Event.Text = "答完了"
	outbound.Timestamp = base.Add(5 * time.Second)
	memory = reduce(memory, outbound, 32)
	if len(memory.OpenLoops) != 0 {
		t.Fatalf("successful outbound did not resolve open loops: %#v", memory.OpenLoops)
	}
	if memory.ActiveTopic != "第四个？" {
		t.Fatalf("outbound text replaced the human topic: %q", memory.ActiveTopic)
	}
}

func TestReduceExpiresStaleOpenLoops(t *testing.T) {
	memory := presencedomain.GroupWorkingMemory{
		GroupID:       1,
		OpenLoops:     []string{"很久以前的问题？"},
		LastUpdatedAt: time.Unix(100, 0),
	}
	record := testEventRecord("new")
	record.Event.Text = "换话题了"
	record.Timestamp = memory.LastUpdatedAt.Add(openLoopTTL + time.Second)

	memory = reduce(memory, record, 32)
	if len(memory.OpenLoops) != 0 {
		t.Fatalf("stale open loops survived TTL: %#v", memory.OpenLoops)
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
