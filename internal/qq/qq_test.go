package qq

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodePreservesAuthorAndLiteralText(t *testing.T) {
	raw := `{"post_type":"message","message_type":"group","group_id":10,"user_id":11,"self_id":12,"message_id":9007199254740993,"message":[{"type":"text","data":{"text":"[CQ:at,qq=12]是文本"}},{"type":"reply","data":{"id":"123"}},{"type":"image","data":{"url":"https://example.com/a","sub_type":1}}],"sender":{"nickname":"昵称","card":"群名片"}}`
	e, ok, err := Decode([]byte(raw), 12, nil)
	if err != nil || !ok || e.MessageID != "9007199254740993" || e.Directed || e.ReplyTo != "123" || e.Name != "群名片" || len(e.Images) != 1 || !e.Images[0].Sticker {
		t.Fatalf("%+v %v %v", e, ok, err)
	}
	e, ok, err = Decode([]byte(`{"post_type":"message","message_type":"group","group_id":10,"user_id":11,"self_id":12,"message_id":2,"message":"你好[CQ:at,qq=12]&#91;原文&#93;"}`), 12, nil)
	if err != nil || !ok || !e.Directed || e.Text != "你好[原文]" {
		t.Fatalf("%+v %v", e, err)
	}
}
func TestSendUsesLiteralSegmentsAndRequiresReceipt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Message []Segment `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Message) != 1 || body.Message[0].Type != "text" || body.Message[0].Data["text"] != "[CQ:at,qq=all]" {
			t.Error("text became an action", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"message_id":9007199254740993}}`))
	}))
	defer server.Close()
	c := Client{URL: server.URL}
	receipt, err := c.Send(context.Background(), 1, "[CQ:at,qq=all]", "", "")
	if err != nil || receipt.MessageID != "9007199254740993" {
		t.Fatal(receipt, err)
	}
}
func TestMediaRejectsPrivateAndNonHTTPURLs(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "http://127.0.0.1/a", "http://[::1]/a", "http://169.254.169.254/a", "http://192.168.1.1/a", "https://user:pass@example.com/a"} {
		if MediaURL(raw) == nil {
			t.Fatal("unsafe media URL", raw)
		}
	}
	if err := MediaURL("https://gchat.qpic.cn/a"); err != nil {
		t.Fatal(err)
	}
}
func TestSendRejectionIsDefinite(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"failed","retcode":100}`))
	}))
	defer srv.Close()
	_, err := (&Client{URL: srv.URL}).Send(context.Background(), 1, "hello", "", "")
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatal(err)
	}
}
