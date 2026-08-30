package dispatcher

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	turnruntime "github.com/phlin/go-agent/internal/runtime/turn"
	normalizersvc "github.com/phlin/go-agent/internal/services/normalizer"
)

type recordingProcessor struct {
	mu         sync.Mutex
	seen       []string
	started    chan string
	continueCh chan struct{}
}

func (p *recordingProcessor) ProcessEnvelope(_ context.Context, envelope conversationdomain.EventEnvelope) (turnruntime.Outcome, error) {
	trace := envelope.Event.MessageID
	p.mu.Lock()
	p.seen = append(p.seen, trace)
	p.mu.Unlock()
	if p.started != nil {
		p.started <- trace
	}
	if p.continueCh != nil {
		<-p.continueCh
	}
	return turnruntime.Outcome{Envelope: envelope}, nil
}

func (p *recordingProcessor) order() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

func TestGroupDispatcherPreservesOrderWithinGroup(t *testing.T) {
	appCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor := &recordingProcessor{started: make(chan string, 2)}
	dispatcher := New(appCtx, normalizersvc.New("test", 123, nil), processor, Config{
		NormalQueueSize: 2,
		JobTimeout:      time.Second,
		IdleTimeout:     time.Minute,
	})

	dispatcher.Dispatch(context.Background(), messagePayload(100, 1, "first"))
	dispatcher.Dispatch(context.Background(), messagePayload(100, 2, "second"))
	awaitTrace(t, processor.started, "1")
	awaitTrace(t, processor.started, "2")

	got := processor.order()
	if len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Fatalf("expected same-group order [1 2], got %v", got)
	}
}

func TestGroupDispatcherRunsDifferentGroupsInParallel(t *testing.T) {
	appCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor := &recordingProcessor{
		started:    make(chan string, 2),
		continueCh: make(chan struct{}),
	}
	dispatcher := New(appCtx, normalizersvc.New("test", 123, nil), processor, Config{
		NormalQueueSize: 2,
		JobTimeout:      time.Second,
		IdleTimeout:     time.Minute,
	})

	dispatcher.Dispatch(context.Background(), messagePayload(100, 1, "first"))
	awaitTrace(t, processor.started, "1")
	dispatcher.Dispatch(context.Background(), messagePayload(200, 2, "second"))
	awaitTrace(t, processor.started, "2")

	close(processor.continueCh)
	got := processor.order()
	if len(got) != 2 {
		t.Fatalf("expected both groups to start, got %v", got)
	}
}

func awaitTrace(t *testing.T, started <-chan string, want string) {
	t.Helper()
	select {
	case got := <-started:
		if got != want {
			t.Fatalf("expected trace %q, got %q", want, got)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for trace %q", want)
	}
}

func messagePayload(groupID, messageID int64, text string) []byte {
	payload, _ := json.Marshal(map[string]any{
		"post_type":    "message",
		"message_type": "group",
		"time":         1700000000,
		"self_id":      123,
		"group_id":     groupID,
		"user_id":      99,
		"message_id":   messageID,
		"raw_message":  text,
		"message": []map[string]any{{
			"type": "text",
			"data": map[string]any{"text": text},
		}},
	})
	return payload
}
