package conversation

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/qq"
)

func TestActorSerializesEventsAndVersionsSnapshots(t *testing.T) {
	var mu sync.Mutex
	var batches [][]qq.Event
	a := NewActor(context.Background(), 7, ActorOptions{Merge: time.Millisecond, Process: func(_ context.Context, events []qq.Event, _ uint64) error {
		mu.Lock()
		batches = append(batches, events)
		mu.Unlock()
		return nil
	}})
	if err := a.Submit(context.Background(), qq.Event{Seq: 1, ID: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Submit(context.Background(), qq.Event{Seq: 2, ID: "b"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	s, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.GroupID != 7 || s.Version != 2 || s.LastSeq != 2 {
		t.Fatalf("unexpected snapshot: %+v", s)
	}
	a.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(batches) == 0 {
		t.Fatal("actor did not process events")
	}
}
