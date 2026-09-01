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
	if envelope.Event.EventID != "onebot:10001:30003" {
		t.Fatalf("event id mismatch: %s", envelope.Event.EventID)
	}
}

func TestNormalizeMissingMessageIDIsStable(t *testing.T) {
	payload := []byte(`{"post_type":"notice","notice_type":"group_recall","time":0,"self_id":1,"group_id":2,"user_id":3}`)
	svc := New("onebot", 1, nil)
	first, err := svc.Normalize(payload)
	if err != nil {
		t.Fatalf("normalize first: %v", err)
	}
	second, err := svc.Normalize(payload)
	if err != nil {
		t.Fatalf("normalize second: %v", err)
	}
	if first.Event.EventID != second.Event.EventID || first.TraceID != second.TraceID {
		t.Fatalf("unstable fallback event ID: first=%q second=%q", first.Event.EventID, second.Event.EventID)
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

func TestNormalizeStickerSegments(t *testing.T) {
	payload := []byte(`{
		"post_type":"message","message_type":"group","time":1700000000,
		"self_id":123456,"group_id":1,"user_id":2,"message_id":"40003",
		"message":[
			{"type":"image","data":{"file":"sticker.gif","url":"http://example.com/sticker.gif","sub_type":1,"summary":"开心小狗"}},
			{"type":"mface","data":{"file":"market.webp","url":"http://example.com/market.webp","summary":"商城表情"}}
		]
	}`)

	envelope, err := New("onebot", 123456, nil).Normalize(payload)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(envelope.Event.Attachments) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(envelope.Event.Attachments))
	}
	for _, attachment := range envelope.Event.Attachments {
		if attachment.Kind != "sticker" {
			t.Fatalf("expected sticker kind, got %s", attachment.Kind)
		}
	}
	if envelope.Event.Attachments[0].PlatformHint != "开心小狗" {
		t.Fatalf("platform hint missing: %+v", envelope.Event.Attachments[0])
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

func TestNormalizePokeTargetBot(t *testing.T) {
	// target_id == selfID → EventPoke，userID 为发起戳的人
	payload := []byte(`{
		"post_type":"notice","notice_type":"notify","sub_type":"poke",
		"time":1700000000,"self_id":123456,"group_id":1,
		"user_id":2,"target_id":123456
	}`)

	svc := New("onebot", 123456, nil)
	envelope, err := svc.Normalize(payload)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	if envelope.Event.Kind != "poke" {
		t.Fatalf("expected poke kind, got %s", envelope.Event.Kind)
	}
	if envelope.Event.UserID != 2 {
		t.Fatalf("expected user_id=2 (poker), got %d", envelope.Event.UserID)
	}
}

func TestNormalizePokeTargetOther(t *testing.T) {
	// target_id != selfID → EventMeta，bot 不应处理
	payload := []byte(`{
		"post_type":"notice","notice_type":"notify","sub_type":"poke",
		"time":1700000000,"self_id":123456,"group_id":1,
		"user_id":2,"target_id":999999
	}`)

	svc := New("onebot", 123456, nil)
	envelope, err := svc.Normalize(payload)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	if envelope.Event.Kind != "meta_event" {
		t.Fatalf("expected meta_event kind for non-target poke, got %s", envelope.Event.Kind)
	}
}
