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

func TestNormalizeRecordSegment(t *testing.T) {
	payload := []byte(`{
		"post_type":"message","message_type":"group","time":1700000000,
		"self_id":123456,"group_id":1,"user_id":2,"message_id":"40001",
		"raw_message":"[语音]",
		"message":[{"type":"record","data":{"file":"voice.silk","url":"http://example.com/voice.silk"}}]
	}`)

	svc := New("onebot", 123456, nil)
	envelope, err := svc.Normalize(payload)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	if len(envelope.Event.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(envelope.Event.Attachments))
	}
	att := envelope.Event.Attachments[0]
	if att.Kind != "audio" {
		t.Fatalf("expected audio kind, got %s", att.Kind)
	}
	if att.MIME != "audio/silk" {
		t.Fatalf("expected audio/silk MIME, got %s", att.MIME)
	}
}

func TestNormalizeFileSegment(t *testing.T) {
	payload := []byte(`{
		"post_type":"message","message_type":"group","time":1700000000,
		"self_id":123456,"group_id":1,"user_id":2,"message_id":"40002",
		"raw_message":"[文件]",
		"message":[{"type":"file","data":{"file":"doc.pdf","url":"http://example.com/doc.pdf"}}]
	}`)

	svc := New("onebot", 123456, nil)
	envelope, err := svc.Normalize(payload)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	if len(envelope.Event.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(envelope.Event.Attachments))
	}
	att := envelope.Event.Attachments[0]
	if att.Kind != "file" {
		t.Fatalf("expected file kind, got %s", att.Kind)
	}
	if att.MIME != "application/octet-stream" {
		t.Fatalf("expected application/octet-stream MIME, got %s", att.MIME)
	}
}
