package napcat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

func TestSenderBuildsSendGroupPayload(t *testing.T) {
	var seenPath string
	var seenBody sendGroupMsgRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&seenBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"message_id":12345}}`))
	}))
	defer server.Close()

	sender := NewSender(server.URL, "", server.Client())
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
	if len(seenBody.Message) < 2 || seenBody.Message[0].Type != "reply" {
		t.Fatalf("unexpected message payload: %#v", seenBody.Message)
	}
}

func TestSenderRecallAndPokePayload(t *testing.T) {
	var paths []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
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
