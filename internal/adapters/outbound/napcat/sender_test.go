package napcat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	napcatsdk "github.com/zjutjh/napcat-sdk"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

func TestSenderBuildsSendGroupPayload(t *testing.T) {
	var seenPath string
	var seenAuthorization string
	var seenBody struct {
		GroupID string                              `json:"group_id"`
		Message []conversationdomain.MessageSegment `json:"message"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenAuthorization = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&seenBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"message_id":12345}}`))
	}))
	defer server.Close()

	sender := NewSender(server.URL, "secret", server.Client())
	receipt, err := sender.Send(context.Background(), replydomain.ActionExecution{
		ActionID: "a1",
		Kind:     policydomain.ActionReply,
		GroupID:  10001,
		Segments: []conversationdomain.MessageSegment{
			{Type: "reply", Data: map[string]any{"id": "30003"}},
			{Type: "text", Data: map[string]any{"text": "回复你了"}},
		},
	})
	if err != nil {
		t.Fatalf("send action: %v", err)
	}
	if !receipt.Sent {
		t.Fatalf("expected sent receipt")
	}
	if seenPath != "/send_group_msg" {
		t.Fatalf("unexpected path: %s", seenPath)
	}
	if seenAuthorization != "Bearer secret" {
		t.Fatalf("unexpected authorization header: %q", seenAuthorization)
	}
	if seenBody.GroupID != "10001" {
		t.Fatalf("unexpected group_id: %q", seenBody.GroupID)
	}
	if len(seenBody.Message) < 2 || seenBody.Message[0].Type != "reply" {
		t.Fatalf("unexpected message payload: %#v", seenBody.Message)
	}
}

func TestSenderRecallAndPokePayload(t *testing.T) {
	var paths []string
	var bodies []map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		bodies = append(bodies, body)
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"message_id":12345}}`))
	}))
	defer server.Close()

	sender := NewSender(server.URL, "", server.Client())
	if _, err := sender.Send(context.Background(), replydomain.ActionExecution{
		ActionID:        "a2",
		Kind:            policydomain.ActionRecall,
		GroupID:         10001,
		TargetMessageID: "12345",
	}); err != nil {
		t.Fatalf("send recall: %v", err)
	}
	if _, err := sender.Send(context.Background(), replydomain.ActionExecution{
		ActionID:     "a3",
		Kind:         policydomain.ActionPokeBack,
		GroupID:      10001,
		TargetUserID: 20002,
	}); err != nil {
		t.Fatalf("send poke: %v", err)
	}

	if len(paths) != 2 || paths[0] != "/delete_msg" || paths[1] != "/group_poke" {
		t.Fatalf("unexpected paths: %#v", paths)
	}
	if bodies[0]["message_id"] != "12345" {
		t.Fatalf("unexpected recall body: %#v", bodies[0])
	}
	if bodies[1]["group_id"] != "10001" || bodies[1]["user_id"] != "20002" {
		t.Fatalf("unexpected poke body: %#v", bodies[1])
	}
}

func TestSenderReturnsSDKAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"failed","retcode":1404,"message":"denied","data":null}`))
	}))
	defer server.Close()

	sender := NewSender(server.URL, "", server.Client())
	_, err := sender.Send(context.Background(), replydomain.ActionExecution{
		ActionID: "a4",
		Kind:     policydomain.ActionReply,
		GroupID:  10001,
		Segments: []conversationdomain.MessageSegment{{Type: "text", Data: map[string]any{"text": "hello"}}},
	})
	var apiErr *napcatsdk.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected SDK APIError, got %v", err)
	}
	if apiErr.RetCode != 1404 {
		t.Fatalf("unexpected retcode: %d", apiErr.RetCode)
	}
}

func TestSenderReactPayload(t *testing.T) {
	var seenPath string
	var seenBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&seenBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"message_id":0}}`))
	}))
	defer server.Close()

	sender := NewSender(server.URL, "", server.Client())
	receipt, err := sender.Send(context.Background(), replydomain.ActionExecution{
		ActionID:        "a-react",
		Kind:            policydomain.ActionReact,
		GroupID:         10001,
		TargetMessageID: "msg-42",
		Meta:            map[string]any{"emoji_id": "128077"},
	})
	if err != nil {
		t.Fatalf("send react: %v", err)
	}
	if !receipt.Sent {
		t.Fatalf("expected sent receipt")
	}
	if seenPath != "/set_msg_emoji_like" {
		t.Fatalf("unexpected path: %s", seenPath)
	}
	if seenBody["message_id"] != "msg-42" {
		t.Fatalf("unexpected message_id: %v", seenBody["message_id"])
	}
	if seenBody["emoji_id"] != "128077" {
		t.Fatalf("unexpected emoji_id: %v", seenBody["emoji_id"])
	}
}
