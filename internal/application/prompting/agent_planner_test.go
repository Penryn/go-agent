package prompting

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
	retrievalsvc "github.com/phlin/go-agent/internal/application/retrieval"
	toolsvc "github.com/phlin/go-agent/internal/application/tools"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
)

func TestAgentPlannerToolLoop(t *testing.T) {
	store := inmemory.NewStore()
	if err := store.UpsertMemory(context.Background(), memorydomain.MemoryRecord{
		MemoryID:   "m1",
		Subject:    "旧梗",
		Content:    "之前聊过这个梗",
		Importance: 0.9,
		CreatedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	mockModel := modeladapter.NewMockChatModel(
		schema.AssistantMessage("", []schema.ToolCall{{
			ID:   "tool-1",
			Type: "function",
			Function: schema.FunctionCall{
				Name:      "query_memory",
				Arguments: `{"query":"旧梗","top_k":1}`,
			},
		}}),
		schema.AssistantMessage("", []schema.ToolCall{{
			ID:   "tool-2",
			Type: "function",
			Function: schema.FunctionCall{
				Name:      "speak_text",
				Arguments: `{"text":"我记得这梗","bubbles":["我记得这梗"]}`,
			},
		}}),
	)

	planner := NewAgentPlanner(
		modeladapter.StaticFactory{MainModel: mockModel},
		toolsvc.NewRuntime(store, store, toolsvc.WithMemoryRetriever(retrievalsvc.New(store, store, nil, nil, retrievalsvc.Config{}))),
		NewComposer(defaultPersona()),
		NewDeterministicPlanner(defaultPersona()),
	)

	plan, err := planner.Plan(context.Background(), sampleSnapshot(), sampleDecision())
	if err != nil {
		t.Fatalf("agent plan: %v", err)
	}

	if len(plan.Bubbles) == 0 || plan.Bubbles[0] != "我记得这梗" {
		t.Fatalf("unexpected bubbles: %#v", plan.Bubbles)
	}
	if len(plan.PlannedActions) == 0 || plan.PlannedActions[0] != policydomain.ActionReply {
		t.Fatalf("unexpected actions: %#v", plan.PlannedActions)
	}
}

func TestAgentPlannerStaySilent(t *testing.T) {
	mockModel := modeladapter.NewMockChatModel(
		schema.AssistantMessage("", []schema.ToolCall{{
			ID:   "tool-1",
			Type: "function",
			Function: schema.FunctionCall{
				Name:      "stay_silent",
				Arguments: `{"reason_code":"too_risky"}`,
			},
		}}),
	)

	planner := NewAgentPlanner(
		modeladapter.StaticFactory{MainModel: mockModel},
		toolsvc.NewRuntime(inmemory.NewStore(), inmemory.NewStore()),
		NewComposer(defaultPersona()),
		NewDeterministicPlanner(defaultPersona()),
	)

	plan, err := planner.Plan(context.Background(), sampleSnapshot(), sampleDecision())
	if err != nil {
		t.Fatalf("agent plan: %v", err)
	}

	if len(plan.PlannedActions) == 0 || plan.PlannedActions[0] != policydomain.ActionSilent {
		t.Fatalf("unexpected actions: %#v", plan.PlannedActions)
	}
}

func TestAgentPlannerCanReplyWhenRuleBaselineWasSilent(t *testing.T) {
	mockModel := modeladapter.NewMockChatModel(
		schema.AssistantMessage("", []schema.ToolCall{{
			ID: "tool-1", Type: "function",
			Function: schema.FunctionCall{Name: "speak_text", Arguments: `{"text":"我想接这句"}`},
		}}),
	)
	planner := NewAgentPlanner(
		modeladapter.StaticFactory{MainModel: mockModel},
		toolsvc.NewRuntime(inmemory.NewStore(), inmemory.NewStore()),
		NewComposer(defaultPersona()), NewDeterministicPlanner(defaultPersona()),
	)
	decision := sampleDecision()
	decision.Action = policydomain.ActionSilent
	plan, err := planner.Plan(context.Background(), sampleSnapshot(), decision)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.PlannedActions) == 0 || plan.PlannedActions[0] != policydomain.ActionReply {
		t.Fatalf("model was not allowed to reply: %+v", plan)
	}
}

func TestAgentPlannerSendMemeReturnsDirectly(t *testing.T) {
	store := inmemory.NewStore()
	if err := store.UpsertMeme(context.Background(), mediadomain.MemeAsset{
		MemeID: "meme-1", GroupID: 1, ObjectKey: "meme.webp", Status: "approved",
	}, mediadomain.MemeDescriptor{MemeID: "meme-1"}); err != nil {
		t.Fatalf("seed meme: %v", err)
	}
	mockModel := modeladapter.NewMockChatModel(schema.AssistantMessage("", []schema.ToolCall{{
		ID: "tool-1", Type: "function", Function: schema.FunctionCall{
			Name: "send_meme", Arguments: `{"meme_id":"meme-1"}`,
		},
	}}))
	planner := NewAgentPlanner(
		modeladapter.StaticFactory{MainModel: mockModel},
		toolsvc.NewRuntime(store, store),
		NewComposer(defaultPersona()),
		NewDeterministicPlanner(defaultPersona()),
	)

	plan, err := planner.Plan(context.Background(), sampleSnapshot(), sampleDecision())
	if err != nil {
		t.Fatalf("agent plan: %v", err)
	}
	if len(plan.PlannedActions) != 1 || plan.PlannedActions[0] != policydomain.ActionMemeOnly {
		t.Fatalf("unexpected actions: %#v", plan.PlannedActions)
	}
	if len(mockModel.Inputs()) != 1 {
		t.Fatalf("send_meme should terminate the tool loop, model calls=%d", len(mockModel.Inputs()))
	}
}

func TestAgentPlannerWithoutToolRuntimeStaysSilent(t *testing.T) {
	planner := NewAgentPlanner(
		modeladapter.StaticFactory{MainModel: modeladapter.NewMockChatModel()},
		nil,
		NewComposer(defaultPersona()),
		NewDeterministicPlanner(defaultPersona()),
	)
	plan, err := planner.Plan(context.Background(), sampleSnapshot(), sampleDecision())
	if err != nil {
		t.Fatalf("planner fallback: %v", err)
	}
	// 模型不可用时保持沉默，不降级到模板话术
	if len(plan.PlannedActions) != 1 || plan.PlannedActions[0] != policydomain.ActionSilent {
		t.Fatalf("expected silent plan, got %+v", plan)
	}
}

func defaultPersona() personadomain.PersonaConfig {
	return personadomain.PersonaConfig{
		ID:                "main",
		Name:              "艾莲酱",
		Description:       "长期在线的大学生，喜欢和大家聊各种话题，尤其是群聊热梗和表情包。平时话不多，但偶尔也会冒个泡，喜欢用表情包表达情绪。",
		SpeechStyle:       "短句，真人感",
		ReplyMaxChars:     80,
		ReplyMaxSentences: 2,
	}
}

func sampleSnapshot() conversationdomain.ContextSnapshot {
	return conversationdomain.ContextSnapshot{
		SnapshotID: "snapshot-1",
		Event: conversationdomain.ConversationEvent{
			GroupID:       1,
			UserID:        2,
			MessageID:     "m-1",
			Text:          "bot 你记得那个梗吗",
			MentionedBot:  true,
			NamedBot:      true,
			TimestampUnix: time.Now().Unix(),
		},
		GroupPolicy: policydomain.GroupPolicy{
			GroupID:       1,
			PresenceLevel: "balanced",
			ToolAllowlist: nil,
		},
		PersonaState: personadomain.PersonaState{
			PersonaID: "main",
			GroupID:   1,
			TalkBias:  0.2,
		},
	}
}

func sampleDecision() policydomain.AutonomyDecision {
	return policydomain.AutonomyDecision{
		DecisionID: "decision-1",
		Action:     policydomain.ActionReply,
	}
}
