package qq

import (
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

	onebot "github.com/zjutjh/onebot-sdk"
	"github.com/zjutjh/onebot-sdk/api"
	"github.com/zjutjh/onebot-sdk/message"
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
type Receipt struct {
	MessageID string `json:"message_id"`
}
type SendError struct {
	Err      error
	Definite bool
}

func (e *SendError) Error() string { return e.Err.Error() }
func (e *SendError) Unwrap() error { return e.Err }
func scalar(raw json.RawMessage) string {
	var value message.StrNum
	if json.Unmarshal(raw, &value) == nil {
		return string(value)
	}
	return ""
}

var cq = regexp.MustCompile(`\[CQ:([^,\]]+)([^\]]*)\]`)

func Decode(payload []byte, self int64, aliases []string) (Event, bool, error) {
	var raw struct {
		Post      string          `json:"post_type"`
		Kind      string          `json:"message_type"`
		Notice    string          `json:"notice_type"`
		Group     message.ID      `json:"group_id"`
		User      message.ID      `json:"user_id"`
		Self      message.ID      `json:"self_id"`
		Time      int64           `json:"time"`
		MessageID message.ID      `json:"message_id"`
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
		self = raw.Self.Int64()
	}
	if raw.Group <= 0 || raw.User.Int64() == self {
		return Event{}, false, nil
	}
	recall := raw.Post == "notice" && raw.Notice == "group_recall"
	if !recall && (raw.Post != "message" || raw.Kind != "group") {
		return Event{}, false, nil
	}
	e := Event{GroupID: raw.Group.Int64(), UserID: raw.User.Int64(), SelfID: self, Name: raw.Sender.Card, At: time.Unix(raw.Time, 0), Recalled: recall}
	// IDs are opaque locators; negative values are valid and must not be ordered.
	if raw.MessageID != 0 {
		e.MessageID = strconv.FormatInt(raw.MessageID.Int64(), 10)
	}
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
	// Preserve wire data: SDK string chains are literal text, and MFaceData
	// does not retain the inbound download URL needed for automatic collection.
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
			e.Text += scalar(s.Data["text"])
		case "at":
			e.Directed = e.Directed || scalar(s.Data["qq"]) == strconv.FormatInt(self, 10)
		case "reply":
			var id message.ID
			if err := json.Unmarshal(s.Data["id"], &id); err != nil {
				return e, false, fmt.Errorf("reply id: %w", err)
			}
			if id != 0 {
				e.ReplyTo = strconv.FormatInt(id.Int64(), 10)
			}
		case "image", "mface":
			u := scalar(s.Data["url"])
			if u != "" {
				e.Images = append(e.Images, Attachment{ID: fmt.Sprintf("%s:%d", e.ID, i), URL: u, Sticker: s.Type == "mface" || scalar(s.Data["sub_type"]) == "1"})
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
	segments := message.Chain{}
	if quote != "" {
		id, err := strconv.ParseInt(quote, 10, 64)
		if err != nil || id == 0 {
			return Receipt{}, &SendError{errors.New("invalid reply id"), true}
		}
		segments = append(segments, message.Reply(id))
	}
	if text != "" {
		segments = append(segments, message.Text(text))
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
		segments = append(segments, message.Image("base64://"+base64.StdEncoding.EncodeToString(b)))
	}
	if group <= 0 || len(segments) == 0 {
		return Receipt{}, &SendError{errors.New("empty or invalid message"), true}
	}
	body, err := api.NewOB11Message(segments)
	if err != nil {
		return Receipt{}, &SendError{err, true}
	}
	client := onebot.NewHTTPClient(c.URL, onebot.WithToken(c.Token), onebot.WithHTTPTimeout(5*time.Second), onebot.WithHTTPClient(c.HTTP))
	result, err := client.API().SendGroupMsg(ctx, api.SendGroupMsgRequest{GroupID: message.ID(group), Message: body})
	if err != nil {
		var rejection *onebot.APIError
		// OneBot retcode 1 with status "async" means the request was accepted
		// for asynchronous processing; only real rejections are definite.
		definite := errors.As(err, &rejection) && (rejection.Status == "failed" || (rejection.RetCode != 0 && rejection.RetCode != 1))
		return Receipt{}, &SendError{err, definite}
	}
	if result.MessageID == 0 {
		return Receipt{}, &SendError{errors.New("OneBot returned no message id"), false}
	}
	return Receipt{strconv.FormatInt(result.MessageID.Int64(), 10)}, nil
}
func (c *Client) Receive(ctx context.Context, handle func(context.Context, []byte) error) error {
	delay := time.Second
	for ctx.Err() == nil {
		// Only core group messages and recalls are consumed, so event-only
		// WebSocket endpoints need no backend detection or extension API calls.
		client, err := onebot.DialWebSocket(ctx, c.WSURL,
			onebot.WithToken(c.WSToken), onebot.WithBackend(onebot.BackendGeneric),
			onebot.WithEventBuffer(256), onebot.WithEventDeliveryTimeout(5*time.Second),
			onebot.WithLogger(slog.Default()))
		if err == nil {
			delay = time.Second
			err = c.receive(ctx, client, handle)
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

func (c *Client) receive(ctx context.Context, client *onebot.Client, handle func(context.Context, []byte) error) error {
	c.Connected.Store(true)
	defer c.Connected.Store(false)
	defer func() { _ = client.Close() }()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e, ok := <-client.Events():
			if !ok {
				return client.Err()
			}
			if err := handle(ctx, e.Raw()); err != nil {
				slog.Error("receive event", "error", err)
			}
		}
	}
}
