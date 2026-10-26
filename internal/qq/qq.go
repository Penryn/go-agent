package qq

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

type Attachment struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Sticker bool   `json:"sticker"`
}
type Event struct {
	Seq       int64        `json:"seq"`
	ID        string       `json:"id"`
	MessageID string       `json:"message_id"`
	GroupID   int64        `json:"group_id"`
	UserID    int64        `json:"user_id"`
	SelfID    int64        `json:"self_id"`
	Name      string       `json:"name"`
	Text      string       `json:"text"`
	ReplyTo   string       `json:"reply_to,omitempty"`
	Directed  bool         `json:"directed"`
	Outbound  bool         `json:"outbound"`
	Recalled  bool         `json:"recalled"`
	At        time.Time    `json:"at"`
	Images    []Attachment `json:"images,omitempty"`
}
type Segment struct {
	Type string            `json:"type"`
	Data map[string]string `json:"data"`
}
type Receipt struct {
	MessageID string `json:"message_id"`
}
type SendError struct {
	Err      error
	Definite bool
}

func (e *SendError) Error() string { return e.Err.Error() }
func (e *SendError) Unwrap() error { return e.Err }
func ID(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return string(n)
	}
	return ""
}

var cq = regexp.MustCompile(`\[CQ:([^,\]]+)([^\]]*)\]`)

func Decode(payload []byte, self int64, aliases []string) (Event, bool, error) {
	var raw struct {
		Post      string          `json:"post_type"`
		Kind      string          `json:"message_type"`
		Notice    string          `json:"notice_type"`
		Group     int64           `json:"group_id"`
		User      int64           `json:"user_id"`
		Self      int64           `json:"self_id"`
		Time      int64           `json:"time"`
		MessageID json.RawMessage `json:"message_id"`
		Message   json.RawMessage `json:"message"`
		Sender    struct {
			Nickname string `json:"nickname"`
			Card     string `json:"card"`
		} `json:"sender"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Event{}, false, err
	}
	if self == 0 {
		self = raw.Self
	}
	if raw.Group <= 0 || raw.User == self {
		return Event{}, false, nil
	}
	recall := raw.Post == "notice" && raw.Notice == "group_recall"
	if !recall && (raw.Post != "message" || raw.Kind != "group") {
		return Event{}, false, nil
	}
	e := Event{GroupID: raw.Group, UserID: raw.User, SelfID: self, MessageID: ID(raw.MessageID), Name: raw.Sender.Card, At: time.Unix(raw.Time, 0), Recalled: recall}
	if e.Name == "" {
		e.Name = raw.Sender.Nickname
	}
	if raw.Time == 0 {
		e.At = time.Now()
	}
	if e.MessageID != "" {
		e.ID = fmt.Sprintf("%s:%d:%s", raw.Post, e.GroupID, e.MessageID)
	} else {
		h := sha256.Sum256(payload)
		e.ID = fmt.Sprintf("raw:%x", h[:16])
	}
	if recall {
		return e, true, nil
	}
	var segments []struct {
		Type string                     `json:"type"`
		Data map[string]json.RawMessage `json:"data"`
	}
	var text string
	if json.Unmarshal(raw.Message, &text) == nil {
		// OneBot string messages contain CQ codes; preserve text as literal segments.
		last := 0
		for _, m := range cq.FindAllStringSubmatchIndex(text, -1) {
			if m[0] > last {
				e.Text += unescape(text[last:m[0]])
			}
			typ := text[m[2]:m[3]]
			data := map[string]json.RawMessage{}
			for _, kv := range strings.Split(strings.TrimPrefix(text[m[4]:m[5]], ","), ",") {
				k, v, ok := strings.Cut(kv, "=")
				if ok {
					data[k], _ = json.Marshal(unescape(v))
				}
			}
			segments = append(segments, struct {
				Type string                     `json:"type"`
				Data map[string]json.RawMessage `json:"data"`
			}{typ, data})
			last = m[1]
		}
		e.Text += unescape(text[last:])
	} else if err := json.Unmarshal(raw.Message, &segments); err != nil {
		return e, false, fmt.Errorf("message segments: %w", err)
	}
	for i, s := range segments {
		switch s.Type {
		case "text":
			e.Text += ID(s.Data["text"])
		case "at":
			e.Directed = e.Directed || ID(s.Data["qq"]) == strconv.FormatInt(self, 10)
		case "reply":
			e.ReplyTo = ID(s.Data["id"])
		case "image", "mface":
			u := ID(s.Data["url"])
			if u != "" {
				e.Images = append(e.Images, Attachment{ID: fmt.Sprintf("%s:%d", e.ID, i), URL: u, Sticker: s.Type == "mface" || ID(s.Data["sub_type"]) == "1"})
			}
		}
	}
	e.Text = strings.TrimSpace(e.Text)
	for _, a := range aliases {
		if a != "" && strings.Contains(strings.ToLower(e.Text), strings.ToLower(a)) {
			e.Directed = true
		}
	}
	return e, true, nil
}
func unescape(s string) string {
	return strings.NewReplacer("&#91;", "[", "&#93;", "]", "&#44;", ",", "&amp;", "&").Replace(s)
}

type Client struct {
	URL, Token, WSURL, WSToken string
	HTTP                       *http.Client
	Connected                  atomic.Bool
}

func (c *Client) Send(ctx context.Context, group int64, text, quote, path string) (Receipt, error) {
	segments := []Segment{}
	if quote != "" {
		segments = append(segments, Segment{"reply", map[string]string{"id": quote}})
	}
	if text != "" {
		segments = append(segments, Segment{"text", map[string]string{"text": text}})
	}
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return Receipt{}, &SendError{err, true}
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, 16<<20+1))
		if err != nil || len(b) > 16<<20 {
			return Receipt{}, &SendError{errors.New("invalid sticker file"), true}
		}
		segments = append(segments, Segment{"image", map[string]string{"file": "base64://" + base64.StdEncoding.EncodeToString(b)}})
	}
	if group <= 0 || len(segments) == 0 {
		return Receipt{}, &SendError{errors.New("empty or invalid message"), true}
	}
	body, err := json.Marshal(map[string]any{"group_id": group, "message": segments})
	if err != nil {
		return Receipt{}, &SendError{err, true}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.URL, "/")+"/send_group_msg", bytes.NewReader(body))
	if err != nil {
		return Receipt{}, &SendError{err, true}
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Receipt{}, &SendError{err, false}
	}
	defer resp.Body.Close()
	var result struct {
		Status string `json:"status"`
		Code   int    `json:"retcode"`
		Data   struct {
			ID json.RawMessage `json:"message_id"`
		} `json:"data"`
	}
	if resp.StatusCode != 200 {
		return Receipt{}, &SendError{fmt.Errorf("OneBot HTTP %d", resp.StatusCode), false}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return Receipt{}, &SendError{err, false}
	}
	if result.Code != 0 || result.Status == "failed" {
		return Receipt{}, &SendError{fmt.Errorf("OneBot rejected message (%d)", result.Code), true}
	}
	id := ID(result.Data.ID)
	if id == "" {
		return Receipt{}, &SendError{errors.New("OneBot returned no message id"), false}
	}
	return Receipt{id}, nil
}
func (c *Client) Receive(ctx context.Context, handle func(context.Context, []byte) error) error {
	delay := time.Second
	for ctx.Err() == nil {
		headers := http.Header{}
		if c.WSToken != "" {
			headers.Set("Authorization", "Bearer "+c.WSToken)
		}
		conn, _, err := websocket.Dial(ctx, c.WSURL, &websocket.DialOptions{HTTPHeader: headers})
		if err == nil {
			c.Connected.Store(true)
			delay = time.Second
			conn.SetReadLimit(2 << 20)
			for ctx.Err() == nil {
				_, b, readErr := conn.Read(ctx)
				if readErr != nil {
					err = readErr
					break
				}
				if err := handle(ctx, b); err != nil {
					slog.Error("receive event", "error", err)
				}
			}
			c.Connected.Store(false)
			_ = conn.CloseNow()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("QQ connection unavailable", "error", err, "retry_in", delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay = min(delay*2, 30*time.Second)
	}
	return ctx.Err()
}
