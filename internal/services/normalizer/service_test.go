package normalizer

import (
	"os"
	"testing"
)

func TestNormalizeMentionEvent(t *testing.T) {
	payload, err := os.ReadFile("../../../tests/testdata/mention_event.json")
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}

	svc := New("onebot", 123456, []string{"bot"})
	envelope, err := svc.Normalize(payload)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	if !envelope.Event.MentionedBot {
		t.Fatalf("expected mentioned bot")
	}
	if !envelope.Event.NamedBot {
		t.Fatalf("expected named bot")
	}
	if envelope.Event.MessageID != "30003" {
		t.Fatalf("message id mismatch: %s", envelope.Event.MessageID)
	}
}
