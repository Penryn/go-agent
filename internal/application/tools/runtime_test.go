package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/phlin/go-agent/internal/testsupport"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

type externalTestTool struct{}

func (externalTestTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "mcp_web_search"}, nil
}

func TestToolSchemas(t *testing.T) {
	store := testsupport.NewStore(t)
	_ = store.SaveMemberProfile(context.Background(), profiledomain.MemberProfile{
		Stats: profiledomain.MemberStats{GroupID: 1, UserID: 2, Nickname: "alice"},
	})
	runtime := NewRuntime(store, store, WithProfileStore(store))
	tools := runtime.Tools(replydomain.ToolContext{
		GroupID:              1,
		UserID:               2,
		AllowedTools:         nil,
		TriggerType:          "poke_reply",
		RecallableMessageIDs: []string{"bot-1"},
	})

	names := map[string]tool.BaseTool{}
	for _, candidate := range tools {
		info, err := candidate.Info(context.Background())
		if err != nil {
			t.Fatalf("tool info: %v", err)
		}
		names[info.Name] = candidate
	}

	for _, name := range []string{"speak_text", "search_meme", "stay_silent", "send_meme", "quote_reply", "query_member_profile", "update_persona_fact", "repair_message", "poke_member"} {
		if _, ok := names[name]; !ok {
			t.Fatalf("expected tool %s", name)
		}
	}
}

func TestContextualTerminalTools(t *testing.T) {
	runtime := NewRuntime(testsupport.NewStore(t), testsupport.NewStore(t))
	base := runtime.TerminalTools(replydomain.ToolContext{})
	if base["poke_member"] || base["repair_message"] {
		t.Fatalf("contextual tools leaked into a normal turn: %#v", base)
	}
	contextual := runtime.TerminalTools(replydomain.ToolContext{
		TriggerType:          "poke_reply",
		RecallableMessageIDs: []string{"bot-1"},
	})
	if !contextual["poke_member"] || !contextual["repair_message"] {
		t.Fatalf("contextual terminal tools missing: %#v", contextual)
	}
}

func TestRepairMessageOnlyAcceptsRecentBotMessage(t *testing.T) {
	candidate := newRepairMessageTool(replydomain.ToolContext{RecallableMessageIDs: []string{"bot-1"}})
	if _, err := candidate.InvokableRun(context.Background(), `{"message_id":"other"}`); err == nil {
		t.Fatal("expected a non-recallable message to be rejected")
	}
	raw, err := candidate.InvokableRun(context.Background(), `{"message_id":"bot-1","corrected_text":"改一下"}`)
	if err != nil {
		t.Fatalf("repair recent message: %v", err)
	}
	plan, ok, err := ParseTerminalPlan("decision-1", "repair_message", raw, replydomain.ToolContext{})
	if err != nil || !ok || plan.PlannedActions[0] != "repair" {
		t.Fatalf("unexpected repair plan: plan=%+v ok=%v err=%v", plan, ok, err)
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
	runtime := NewRuntime(testsupport.NewStore(t), testsupport.NewStore(t))
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

func TestUpdatePersonaFactAuthorizationAndProvenance(t *testing.T) {
	store := testsupport.NewStore(t)
	now := time.Now().Truncate(time.Second)
	session := replydomain.ToolContext{
		GroupID:              1,
		UserID:               2,
		TriggerEventID:       "event-1",
		TriggerTimestampUnix: now.Unix(),
		Budget:               map[string]int{},
	}
	verifiedTool := newUpdatePersonaFactTool(store, session, "main", []int64{2})
	raw, err := verifiedTool.InvokableRun(context.Background(), `{
		"key":"school_status",
		"value":"已经正式开课，正在适应课程",
		"source_kind":"owner_statement",
		"evidence_event_id":"event-1",
		"confidence":1
	}`)
	if err != nil {
		t.Fatalf("update verified fact: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil || result["status"] != personadomain.PersonaFactVerified {
		t.Fatalf("unexpected verified result: raw=%s err=%v", raw, err)
	}
	facts, err := store.CurrentPersonaFacts(context.Background(), "main", now.Add(time.Minute))
	if err != nil || len(facts) != 1 || facts[0].Value != "已经正式开课，正在适应课程" || facts[0].SourceUserID != 2 {
		t.Fatalf("unexpected stored facts: facts=%+v err=%v", facts, err)
	}

	unauthorized := newUpdatePersonaFactTool(store, replydomain.ToolContext{
		GroupID:        1,
		UserID:         9,
		TriggerEventID: "event-2",
		Budget:         map[string]int{},
	}, "main", []int64{2})
	raw, err = unauthorized.InvokableRun(context.Background(), `{
		"key":"school_status",
		"value":"已经大三",
		"source_kind":"owner_statement",
		"evidence_event_id":"event-2"
	}`)
	if err != nil {
		t.Fatalf("reject unauthorized owner: %v", err)
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil || result["reason"] != "owner_not_authorized" {
		t.Fatalf("unexpected unauthorized result: raw=%s err=%v", raw, err)
	}

	reported := newUpdatePersonaFactTool(store, replydomain.ToolContext{
		GroupID:              1,
		UserID:               9,
		TriggerEventID:       "event-3",
		TriggerTimestampUnix: now.Unix(),
		Budget:               map[string]int{},
	}, "main", []int64{2})
	raw, err = reported.InvokableRun(context.Background(), `{
		"key":"school_familiarity",
		"value":"听说已经基本认得教学楼",
		"source_kind":"group_report",
		"evidence_event_id":"event-3",
		"ttl_hours":24
	}`)
	if err != nil {
		t.Fatalf("update reported fact: %v", err)
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil || result["status"] != personadomain.PersonaFactReported {
		t.Fatalf("unexpected reported result: raw=%s err=%v", raw, err)
	}
}
