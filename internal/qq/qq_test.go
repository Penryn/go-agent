package qq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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

func TestMarkMessageReadSendsPreciseIntegerID(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/mark_msg_as_read" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("unexpected API request", r.Method, r.URL.Path)
		}
		// An int64 beyond the JavaScript safe range must stay a decimal
		// integer, never a float literal.
		if !strings.Contains(string(raw), `"message_id":9223372036854775807`) {
			t.Errorf("message id was not sent as a precise integer: %s", raw)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":null}`))
	}))
	defer server.Close()
	if err := (&Client{URL: server.URL, Token: "test-token"}).MarkMessageRead(context.Background(), "9223372036854775807"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("mark read was retried", calls.Load())
	}
}

func TestMarkMessageReadFailureSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		status         int
		definite       bool
	}{
		{name: "rejected", response: `{"status":"failed","retcode":1200,"message":"denied"}`, definite: true},
		{name: "unknown action", response: `{"status":"failed","retcode":1404,"message":"unknown action"}`, definite: true},
		{name: "async is not a read confirmation", response: `{"status":"async","retcode":1}`},
		{name: "gateway failure", status: http.StatusBadGateway, response: `gateway failed`},
		{name: "malformed envelope", response: `{`},
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
			err := (&Client{URL: server.URL}).MarkMessageRead(context.Background(), "55")
			var sendErr *SendError
			if !errors.As(err, &sendErr) || sendErr.Definite != tc.definite {
				t.Fatalf("error=%+v, definite=%v", err, tc.definite)
			}
			if calls.Load() != 1 {
				t.Fatal("mark read was retried", calls.Load())
			}
		})
	}
}

func TestRefreshMessageValidatesTarget(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		status         int
		wantErr        bool
		definite       bool
	}{
		{name: "group match", response: `{"status":"ok","retcode":0,"data":{"message_id":9007199254740993,"message_type":"group","group_id":10}}`},
		{name: "string group id", response: `{"status":"ok","retcode":0,"data":{"message_id":9007199254740993,"message_type":"group","group_id":"10"}}`},
		{name: "group unreported", response: `{"status":"ok","retcode":0,"data":{"message_id":9007199254740993,"message_type":"group","group_id":null}}`},
		{name: "message id mismatch", response: `{"status":"ok","retcode":0,"data":{"message_id":1,"message_type":"group","group_id":10}}`, wantErr: true, definite: true},
		{name: "message id missing", response: `{"status":"ok","retcode":0,"data":{"message_type":"group","group_id":10}}`, wantErr: true, definite: true},
		{name: "other group", response: `{"status":"ok","retcode":0,"data":{"message_id":9007199254740993,"message_type":"group","group_id":11}}`, wantErr: true, definite: true},
		{name: "private target", response: `{"status":"ok","retcode":0,"data":{"message_id":9007199254740993,"message_type":"private"}}`, wantErr: true, definite: true},
		{name: "message not found", response: `{"status":"failed","retcode":1200,"message":"message not found"}`, wantErr: true, definite: true},
		{name: "gateway failure", status: http.StatusBadGateway, response: `gateway failed`, wantErr: true},
		{name: "malformed envelope", response: `{`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/get_msg" {
					t.Error("unexpected API request", r.URL.Path)
				}
				raw, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(raw), `"message_id":9007199254740993`) {
					t.Errorf("message id was not sent as a precise integer: %s", raw)
				}
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				_, _ = w.Write([]byte(tc.response))
			}))
			defer server.Close()
			err := (&Client{URL: server.URL}).RefreshMessage(context.Background(), "9007199254740993", 10)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("unexpected refresh failure: %v", err)
				}
				return
			}
			var sendErr *SendError
			if !errors.As(err, &sendErr) || sendErr.Definite != tc.definite {
				t.Fatalf("error=%+v, wantErr=%v, definite=%v", err, tc.wantErr, tc.definite)
			}
		})
	}
}

func TestQuotePreconditionsRejectInvalidIDs(t *testing.T) {
	// Nothing may reach the platform for an invalid quote id; an accidental
	// call would surface as a non-definite transport error.
	c := Client{URL: "http://127.0.0.1:1"}
	for _, id := range []string{"", "0", "not-an-id", "9223372036854775808"} {
		for _, err := range []error{
			c.MarkMessageRead(context.Background(), id),
			c.RefreshMessage(context.Background(), id, 1),
		} {
			var sendErr *SendError
			if !errors.As(err, &sendErr) || !sendErr.Definite {
				t.Fatal("invalid quote id reached the platform", id, err)
			}
		}
	}
	for _, err := range []error{
		c.React(context.Background(), "0", "76"),
		c.React(context.Background(), "55", "赞"),
		c.Poke(context.Background(), 0, 10),
		c.Poke(context.Background(), 1, 0),
	} {
		var sendErr *SendError
		if !errors.As(err, &sendErr) || !sendErr.Definite {
			t.Fatal("invalid action target reached the platform", err)
		}
	}
}

func TestReactSendsPreciseIDsAndFailuresArePreconditions(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/set_msg_emoji_like" {
			t.Error("unexpected API request", r.URL.Path)
		}
		if !strings.Contains(string(raw), `"message_id":9007199254740993`) || !strings.Contains(string(raw), `"emoji_id":76`) {
			t.Errorf("reaction ids were not sent as precise integers: %s", raw)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{}}`))
	}))
	defer server.Close()
	if err := (&Client{URL: server.URL}).React(context.Background(), "9007199254740993", "76"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("react was retried", calls.Load())
	}
	for _, tc := range []struct {
		name, response string
		status         int
		definite       bool
	}{
		{name: "rejected", response: `{"status":"failed","retcode":1200,"message":"denied"}`, definite: true},
		{name: "async is not a reaction confirmation", response: `{"status":"async","retcode":1}`},
		{name: "gateway failure", status: http.StatusBadGateway, response: `gateway failed`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				_, _ = w.Write([]byte(tc.response))
			}))
			defer srv.Close()
			err := (&Client{URL: srv.URL}).React(context.Background(), "55", "76")
			var sendErr *SendError
			if !errors.As(err, &sendErr) || sendErr.Definite != tc.definite {
				t.Fatalf("error=%+v, definite=%v", err, tc.definite)
			}
		})
	}
}

func TestPokeRefreshesMemberThenPokes(t *testing.T) {
	var pokes atomic.Int32
	actions := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actions <- r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/get_group_member_info":
			if !strings.Contains(string(raw), `"no_cache":true`) {
				t.Errorf("member refresh must bypass cache: %s", raw)
			}
			_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"user_id":10,"group_id":1}}`))
		case "/group_poke":
			if !strings.Contains(string(raw), `"group_id":1`) || !strings.Contains(string(raw), `"user_id":10`) {
				t.Errorf("invalid poke request: %s", raw)
			}
			pokes.Add(1)
			_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":null}`))
		default:
			t.Error("unexpected API request", r.URL.Path)
		}
	}))
	defer server.Close()
	if err := (&Client{URL: server.URL}).Poke(context.Background(), 1, 10); err != nil {
		t.Fatal(err)
	}
	if pokes.Load() != 1 {
		t.Fatal("poke was retried", pokes.Load())
	}
	if got := drainActions(actions); !slices.Equal(got, []string{"/get_group_member_info", "/group_poke"}) {
		t.Fatal("unexpected poke order", got)
	}
}

func TestPokeBlockedWhenMembershipRefreshFails(t *testing.T) {
	var pokes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/get_group_member_info" {
			_, _ = w.Write([]byte(`{"status":"failed","retcode":1200,"message":"member not found"}`))
			return
		}
		if r.URL.Path == "/group_poke" {
			pokes.Add(1)
		}
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":null}`))
	}))
	defer server.Close()
	err := (&Client{URL: server.URL}).Poke(context.Background(), 1, 10)
	var sendErr *SendError
	if !errors.As(err, &sendErr) || !sendErr.Definite {
		t.Fatal("member refresh failure must be a definite precondition failure", err)
	}
	if pokes.Load() != 0 {
		t.Fatal("poke ran without membership confirmation")
	}
}

func drainActions(actions chan string) []string {
	var got []string
	for {
		select {
		case a := <-actions:
			got = append(got, a)
		default:
			return got
		}
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
