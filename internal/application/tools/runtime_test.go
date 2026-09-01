package tools

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

type externalTestTool struct{}

func (externalTestTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "mcp_web_search"}, nil
}

func TestToolSchemas(t *testing.T) {
	store := inmemory.NewStore()
	_ = store.SaveMemberProfile(context.Background(), profiledomain.MemberProfile{
		Stats: profiledomain.MemberStats{GroupID: 1, UserID: 2, Nickname: "alice"},
	})
	runtime := NewRuntime(store, store, WithProfileStore(store))
	tools := runtime.Tools(replydomain.ToolContext{
		GroupID:      1,
		UserID:       2,
		AllowedTools: nil,
	})

	names := map[string]tool.BaseTool{}
	for _, candidate := range tools {
		info, err := candidate.Info(context.Background())
		if err != nil {
			t.Fatalf("tool info: %v", err)
		}
		names[info.Name] = candidate
	}

	for _, name := range []string{"speak_text", "search_meme", "stay_silent", "send_meme", "quote_reply", "query_member_profile", "recall_recent_message", "poke_member"} {
		if _, ok := names[name]; !ok {
			t.Fatalf("expected tool %s", name)
		}
	}
}

func TestTerminalPlan(t *testing.T) {
	plan, ok, err := ParseTerminalPlan("decision-1", "send_meme", `{"tool":"send_meme","meme_id":"m1","reply_to_message_id":"r1","caption":"收下"}`, replydomain.ToolContext{})
	if err != nil || !ok {
		t.Fatalf("parse terminal plan: ok=%v err=%v", ok, err)
	}
	if plan.PlannedActions[0] != "meme_only" {
		t.Fatalf("unexpected planned action: %#v", plan.PlannedActions)
	}
}

func TestExternalToolsRequireExplicitAllowlist(t *testing.T) {
	runtime := NewRuntime(inmemory.NewStore(), inmemory.NewStore())
	if err := runtime.RegisterTools(context.Background(), externalTestTool{}); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range runtime.Tools(replydomain.ToolContext{}) {
		info, _ := candidate.Info(context.Background())
		if info.Name == "mcp_web_search" {
			t.Fatal("external tool was exposed without an explicit allowlist")
		}
	}
	for _, candidate := range runtime.Tools(replydomain.ToolContext{AllowedTools: []string{"mcp_web_search"}}) {
		info, _ := candidate.Info(context.Background())
		if info.Name == "mcp_web_search" {
			return
		}
	}
	t.Fatal("explicitly allowed external tool was not exposed")
}
