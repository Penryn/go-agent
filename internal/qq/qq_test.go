package qq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/zjutjh/onebot-sdk/message"
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
			Message message.Chain `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/send_group_msg" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("unexpected API request", r.Method, r.URL.Path)
		}
		if len(body.Message) != 1 || body.Message[0].Data != (message.TextData{Text: "[CQ:at,qq=all]"}) {
			t.Error("text became an action", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"message_id":9007199254740993}}`))
	}))
	defer server.Close()
	c := Client{URL: server.URL, Token: "test-token"}
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
	var sendErr *SendError
	if !errors.As(err, &sendErr) || !sendErr.Definite {
		t.Fatal(err)
	}
}

func TestDecodeStringAndSignedIDs(t *testing.T) {
	for _, id := range []string{"9007199254740993", `"9007199254740993"`, "-2147483648", `"-2147483648"`} {
		t.Run(id, func(t *testing.T) {
			raw := fmt.Sprintf(`{"post_type":"message","message_type":"group","group_id":"10","user_id":"11","self_id":"12","message_id":%s,"message":[{"type":"reply","data":{"id":%s}},{"type":"at","data":{"qq":12}},{"type":"mface","data":{"url":"https://example.com/meme.gif"}}]}`, id, id)
			e, ok, err := Decode([]byte(raw), 0, nil)
			want := strings.Trim(id, `"`)
			if err != nil || !ok || e.GroupID != 10 || e.UserID != 11 || e.SelfID != 12 || e.MessageID != want || e.ReplyTo != want || e.ID != "message:10:"+want || !e.Directed || len(e.Images) != 1 || !e.Images[0].Sticker {
				t.Fatalf("%+v, ok=%v, err=%v", e, ok, err)
			}
			recall := fmt.Sprintf(`{"post_type":"notice","notice_type":"group_recall","group_id":"10","user_id":"11","self_id":"12","message_id":%s}`, id)
			e, ok, err = Decode([]byte(recall), 0, nil)
			if err != nil || !ok || !e.Recalled || e.MessageID != want || e.ID != "notice:10:"+want {
				t.Fatalf("recall: %+v, %v, %v", e, ok, err)
			}
		})
	}
	for _, id := range []string{"1.5", "1e3", `"9223372036854775808"`} {
		raw := fmt.Sprintf(`{"post_type":"message","message_type":"group","group_id":10,"user_id":11,"message_id":%s,"message":[]}`, id)
		if _, ok, err := Decode([]byte(raw), 12, nil); ok || err == nil {
			t.Fatalf("accepted invalid ID %s", id)
		}
	}
}

func TestSendReceiptAndFailureSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, response, wantID string
		status                 int
		definite               bool
	}{
		{name: "string ID", response: `{"status":"ok","retcode":0,"data":{"message_id":"9007199254740993"}}`, wantID: "9007199254740993"},
		{name: "negative ID", response: `{"status":"ok","retcode":0,"data":{"message_id":-2147483648}}`, wantID: "-2147483648"},
		{name: "zero ID", response: `{"status":"ok","retcode":0,"data":{"message_id":0}}`},
		{name: "missing ID", response: `{"status":"ok","retcode":0,"data":{}}`},
		{name: "null data", response: `{"status":"ok","retcode":0,"data":null}`},
		{name: "invalid ID", response: `{"status":"ok","retcode":0,"data":{"message_id":1.5}}`},
		{name: "malformed response", response: `{`},
		{name: "missing envelope", response: `{"data":{"message_id":1}}`},
		{name: "HTTP error", status: http.StatusBadGateway, response: `gateway failed`},
		{name: "rejected", response: `{"status":"failed","retcode":100}`, definite: true},
		{name: "async accepted", response: `{"status":"async","retcode":1}`},
		{name: "failed with async retcode", response: `{"status":"failed","retcode":1}`, definite: true},
		{name: "business retcode", response: `{"status":"ok","retcode":1200}`, definite: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				_, _ = w.Write([]byte(tc.response))
			}))
			defer server.Close()
			receipt, err := (&Client{URL: server.URL}).Send(context.Background(), 1, "hello", "", "")
			if tc.wantID != "" {
				if err != nil || receipt.MessageID != tc.wantID {
					t.Fatal(receipt, err)
				}
			} else {
				var sendErr *SendError
				if !errors.As(err, &sendErr) || sendErr.Definite != tc.definite || receipt.MessageID != "" {
					t.Fatalf("receipt=%+v, error=%+v", receipt, err)
				}
			}
			if calls.Load() != 1 {
				t.Fatal("send was retried", calls.Load())
			}
		})
	}
}

func TestSendQuoteAndSticker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sticker.gif")
	if err := os.WriteFile(path, []byte("GIF89a"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			GroupID message.ID    `json:"group_id"`
			Message message.Chain `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.GroupID != 9007199254740993 || len(body.Message) != 2 || body.Message[0].Data != (message.ReplyData{ID: -2147483648}) || body.Message[1].Data != (message.ImageData{File: "base64://R0lGODlh"}) {
			t.Error("invalid quote or image", body)
		}
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"message_id":-1}}`))
	}))
	defer server.Close()
	c := Client{URL: server.URL}
	if _, err := c.Send(context.Background(), 9007199254740993, "", "-2147483648", path); err != nil {
		t.Fatal(err)
	}
	for _, quote := range []string{"0", "not-an-id", "9223372036854775808"} {
		_, err := c.Send(context.Background(), 1, "hello", quote, "")
		var sendErr *SendError
		if !errors.As(err, &sendErr) || !sendErr.Definite {
			t.Fatal("invalid quote reached API", quote, err)
		}
	}
}

func TestReceiveOrdersEventsReconnectsAndCancels(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var connections atomic.Int32
	firstHandled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ws-token" {
			t.Error("missing WebSocket token")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		ids := []int{1, 2}
		first := connections.Add(1) == 1
		if !first {
			ids = []int{3}
		}
		for _, id := range ids {
			raw := fmt.Sprintf(`{"post_type":"message","message_type":"group","group_id":"10","user_id":"11","self_id":"12","message_id":%d,"message":[{"type":"text","data":{"text":"hello"}}]}`, id)
			if err := conn.Write(ctx, websocket.MessageText, []byte(raw)); err != nil {
				t.Error(err)
				return
			}
		}
		if first {
			select {
			case <-firstHandled:
				_ = conn.Close(websocket.StatusGoingAway, "reconnect")
			case <-ctx.Done():
			}
			return
		}
		// This is an event-only endpoint; no backend detection API is needed.
		if _, _, err := conn.Read(ctx); err == nil {
			t.Error("unexpected API call over event WebSocket")
		}
	}))
	defer server.Close()
	c := Client{WSURL: "ws" + strings.TrimPrefix(server.URL, "http"), WSToken: "ws-token"}
	seen := make(chan string, 3)
	done := make(chan error, 1)
	go func() {
		done <- c.Receive(ctx, func(ctx context.Context, raw []byte) error {
			e, ok, err := Decode(raw, 0, nil)
			if err != nil || !ok || !c.Connected.Load() {
				t.Errorf("invalid delivery: %+v, %v", e, err)
			}
			seen <- e.MessageID
			if e.MessageID == "2" {
				close(firstHandled)
			}
			if e.MessageID == "1" {
				return errors.New("handler failure should not stop intake")
			}
			return nil
		})
	}()
	for _, want := range []string{"1", "2", "3"} {
		select {
		case got := <-seen:
			if got != want {
				t.Fatalf("got ID %s, want %s", got, want)
			}
		case <-ctx.Done():
			t.Fatal("events or reconnect did not finish")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || c.Connected.Load() || connections.Load() != 2 {
			t.Fatal("connection did not close cleanly", err, connections.Load())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("receive did not stop after cancellation")
	}
}
