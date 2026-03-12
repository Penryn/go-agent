package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	"github.com/phlin/go-agent/internal/core/ports"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

type stubSearcher struct{}

func (stubSearcher) Search(_ context.Context, query string, topK int, freshness string) ([]ports.SearchResult, error) {
	return []ports.SearchResult{{
		Title:   query,
		URL:     "https://example.com",
		Snippet: freshness,
	}}, nil
}

func TestToolSchemas(t *testing.T) {
	store := inmemory.NewStore()
	_ = store.SaveMemberProfile(context.Background(), profiledomain.MemberProfile{
		Stats: profiledomain.MemberStats{GroupID: 1, UserID: 2, Nickname: "alice"},
	})
	runtime := NewRuntime(store, store, WithProfileStore(store), WithWebSearcher(stubSearcher{}))
	tools := runtime.Tools(replydomain.ToolContext{
		GroupID:      1,
		UserID:       2,
		AllowedTools: []string{"speak_text", "search_meme", "stay_silent", "send_meme", "quote_reply", "query_member_profile", "web_search", "recall_recent_message", "poke_member"},
	})

	names := map[string]tool.BaseTool{}
	for _, candidate := range tools {
		info, err := candidate.Info(context.Background())
		if err != nil {
			t.Fatalf("tool info: %v", err)
		}
		names[info.Name] = candidate
	}

	for _, name := range []string{"speak_text", "search_meme", "stay_silent", "send_meme", "quote_reply", "query_member_profile", "web_search", "recall_recent_message", "poke_member"} {
		if _, ok := names[name]; !ok {
			t.Fatalf("expected tool %s", name)
		}
	}
}

func TestWebSearchToolAndTerminalPlan(t *testing.T) {
	runtime := NewRuntime(inmemory.NewStore(), inmemory.NewStore(), WithWebSearcher(stubSearcher{}))
	tools := runtime.Tools(replydomain.ToolContext{
		AllowedTools: []string{"web_search", "send_meme"},
	})

	var webTool tool.BaseTool
	for _, candidate := range tools {
		info, _ := candidate.Info(context.Background())
		if info.Name == "web_search" {
			webTool = candidate
			break
		}
	}

	result, err := webTool.(interface {
		InvokableRun(context.Context, string, ...tool.Option) (string, error)
	}).InvokableRun(context.Background(), `{"query":"离谱","top_k":1,"freshness":"day"}`)
	if err != nil {
		t.Fatalf("run web_search: %v", err)
	}
	if !strings.Contains(result, "https://example.com") {
		t.Fatalf("unexpected web_search result: %s", result)
	}

	plan, ok, err := ParseTerminalPlan("decision-1", "send_meme", `{"tool":"send_meme","meme_id":"m1","reply_to_message_id":"r1","caption":"收下"}`, replydomain.ToolContext{})
	if err != nil || !ok {
		t.Fatalf("parse terminal plan: ok=%v err=%v", ok, err)
	}
	if plan.PlannedActions[0] != "meme_only" {
		t.Fatalf("unexpected planned action: %#v", plan.PlannedActions)
	}
}
