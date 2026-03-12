package napcat

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerProcessesPayload(t *testing.T) {
	var seen []byte
	handler := NewHandler("", func(_ context.Context, payload []byte) (any, error) {
		seen = append([]byte(nil), payload...)
		return map[string]any{"ok": true}, nil
	})

	req := httptest.NewRequest(http.MethodPost, "/onebot/events", bytes.NewBufferString(`{"post_type":"message"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if string(seen) != `{"post_type":"message"}` {
		t.Fatalf("unexpected payload: %s", string(seen))
	}
}

func TestHandlerAuthorization(t *testing.T) {
	handler := NewHandler("secret", func(_ context.Context, payload []byte) (any, error) {
		return payload, nil
	})

	req := httptest.NewRequest(http.MethodPost, "/onebot/events", bytes.NewBufferString(`{}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
}
