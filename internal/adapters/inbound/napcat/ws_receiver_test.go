package napcat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestWSReceiverReceivesEvent(t *testing.T) {
	payloadCh := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("unexpected authorization header: %q", got)
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"post_type":"message"}`)); err != nil {
			t.Errorf("write event: %v", err)
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	receiver := NewWSReceiver(wsURL(server.URL), "secret")
	done := make(chan error, 1)
	go func() {
		done <- receiver.Receive(ctx, func(_ context.Context, payload []byte) error {
			payloadCh <- append([]byte(nil), payload...)
			cancel()
			return nil
		})
	}()

	select {
	case payload := <-payloadCh:
		if string(payload) != `{"post_type":"message"}` {
			t.Fatalf("unexpected payload: %s", string(payload))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for websocket payload")
	}

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("unexpected receiver exit: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for receiver shutdown")
	}
}

func TestWSReceiverContinuesAfterHandlerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()

		if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"post_type":"message","message_id":"1"}`)); err != nil {
			t.Errorf("write first event: %v", err)
			return
		}
		if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"post_type":"message","message_id":"2"}`)); err != nil {
			t.Errorf("write second event: %v", err)
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var handled atomic.Int32
	unexpectedPayload := make(chan string, 2)
	receiver := NewWSReceiver(wsURL(server.URL), "")
	done := make(chan error, 1)
	go func() {
		done <- receiver.Receive(ctx, func(_ context.Context, payload []byte) error {
			switch handled.Add(1) {
			case 1:
				if string(payload) != `{"post_type":"message","message_id":"1"}` {
					unexpectedPayload <- string(payload)
				}
				return errors.New("boom")
			case 2:
				if string(payload) != `{"post_type":"message","message_id":"2"}` {
					unexpectedPayload <- string(payload)
				}
				cancel()
			}
			return nil
		})
	}()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("unexpected receiver exit: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for receiver shutdown")
	}

	if got := handled.Load(); got != 2 {
		t.Fatalf("expected receiver to keep processing after handler error, handled=%d", got)
	}
	select {
	case payload := <-unexpectedPayload:
		t.Fatalf("unexpected payload: %s", payload)
	default:
	}
}

func wsURL(raw string) string {
	return "ws" + raw[len("http"):]
}
