package presence

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/application/normalizer"
	"github.com/phlin/go-agent/internal/application/presence/deliberation"
	"github.com/phlin/go-agent/internal/application/presence/group_actor"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
)

type blockedDeliberator struct {
	started, cancelled, release chan struct{}
}

func (d *blockedDeliberator) Deliberate(ctx context.Context, _ conversationdomain.EventEnvelope) (deliberation.Result, error) {
	close(d.started)
	<-ctx.Done()
	close(d.cancelled)
	<-d.release
	return deliberation.Result{}, ctx.Err()
}

func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for lifecycle signal")
	}
}

const lifecyclePayload = `{"post_type":"message","message_type":"group","time":1710000000,"self_id":123456,"group_id":100,"user_id":200,"message_id":"lifecycle","message":[{"type":"text","data":{"text":"hello"}}]}`

func TestCloseWaitsForOnlineAndSynchronousTurns(t *testing.T) {
	for _, online := range []bool{true, false} {
		name := "synchronous"
		if online {
			name = "online"
		}
		t.Run(name, func(t *testing.T) {
			working := group_actor.NewManager()
			d := &blockedDeliberator{started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
			r := New(context.Background(), Dependencies{
				Normalizer: normalizer.New("onebot", 123456, nil), WorkingMemory: working, Deliberator: d,
			}, Config{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(d.release) }) }
			t.Cleanup(func() { release(); _ = r.Close(); _ = working.Close() })
			processed := make(chan error, 1)
			if online {
				if err := r.SubmitRaw(context.Background(), []byte(lifecyclePayload)); err != nil {
					t.Fatal(err)
				}
			} else {
				go func() { _, err := r.ProcessRawEvent(context.Background(), []byte(lifecyclePayload)); processed <- err }()
			}
			awaitSignal(t, d.started)
			closed := make(chan struct{})
			go func() { _ = r.Close(); close(closed) }()
			awaitSignal(t, d.cancelled)
			select {
			case <-closed:
				t.Fatal("Close returned while the model turn was still running")
			case <-time.After(30 * time.Millisecond):
			}
			release()
			awaitSignal(t, closed)
			if !online {
				if err := <-processed; !errors.Is(err, context.Canceled) {
					t.Fatalf("synchronous turn cancellation: %v", err)
				}
			}
			if err := r.SubmitRaw(context.Background(), []byte(lifecyclePayload)); !errors.Is(err, context.Canceled) {
				t.Fatalf("closed runtime admitted an event: %v", err)
			}
			if _, err := r.ProcessRawEvent(context.Background(), []byte(lifecyclePayload)); !errors.Is(err, context.Canceled) {
				t.Fatalf("closed runtime admitted a synchronous event: %v", err)
			}
		})
	}
}

func TestTurnReplacementIsScopedToItsGroup(t *testing.T) {
	working := group_actor.NewManager()
	r := New(context.Background(), Dependencies{WorkingMemory: working}, Config{})
	defer working.Close()
	defer r.Close()
	first, finishFirst, err := r.beginTurn(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer finishFirst()
	other, finishOther, err := r.beginTurn(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer finishOther()
	latest, finishLatest, err := r.beginTurn(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer finishLatest()
	if !errors.Is(first.Err(), context.Canceled) || other.Err() != nil || latest.Err() != nil {
		t.Fatalf("group cancellation leaked: first=%v other=%v latest=%v", first.Err(), other.Err(), latest.Err())
	}
}
