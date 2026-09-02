package prompting

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
	retrievalsvc "github.com/phlin/go-agent/internal/application/retrieval"
	toolsvc "github.com/phlin/go-agent/internal/application/tools"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	"github.com/phlin/go-agent/internal/testsupport"
)

func TestAgentPlannerToolLoop(t *testing.T) {
	store := testsupport.NewStore(t)
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
		toolsvc.NewRuntime(store, toolsvc.WithMemoryRetriever(retrievalsvc.New(store, store, nil, nil, retrievalsvc.Config{}))),
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
	inputs := mockModel.Inputs()
	if len(inputs) == 0 || len(inputs[0]) < 3 {
		t.Fatalf("expected static system, current event and dynamic context messages: %#v", inputs)
	}
	if inputs[0][0].Role != schema.System || !strings.Contains(inputs[0][0].Content, "长期稳定规则层") {
		t.Fatalf("first message is not the stable prefix: %+v", inputs[0][0])
	}
	if inputs[0][1].Role != schema.User || !strings.Contains(inputs[0][1].Content, "bot 你记得那个梗吗") {
		t.Fatalf("second message is not the current event: %+v", inputs[0][1])
	}
	if inputs[0][2].Role != schema.User || !strings.Contains(inputs[0][2].Content, "本轮动态数据") {
		t.Fatalf("dynamic context does not follow the cacheable event prefix: %+v", inputs[0][2])
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
		toolsvc.NewRuntime(testsupport.NewStore(t)),
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
		toolsvc.NewRuntime(testsupport.NewStore(t)),
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
	store := testsupport.NewStore(t)
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
		toolsvc.NewRuntime(store),
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

func TestAgentPlannerRepairsRecentBotMessage(t *testing.T) {
	now := time.Now()
	snapshot := sampleSnapshot()
	snapshot.SelfID = 99
	snapshot.Event.TimestampUnix = now.Unix()
	snapshot.RecentTurns = []conversationdomain.ConversationEvent{{
		EventID:       "outbound-old",
		GroupID:       1,
		UserID:        99,
		MessageID:     "bot-1",
		Kind:          conversationdomain.EventMessage,
		Text:          "说错了",
		TimestampUnix: now.Add(-time.Minute).Unix(),
	}}
	mockModel := modeladapter.NewMockChatModel(schema.AssistantMessage("", []schema.ToolCall{{
		ID: "tool-1", Type: "function", Function: schema.FunctionCall{
			Name: "repair_message", Arguments: `{"message_id":"bot-1","corrected_text":"改成这句"}`,
		},
	}}))
	planner := NewAgentPlanner(
		modeladapter.StaticFactory{MainModel: mockModel},
		toolsvc.NewRuntime(testsupport.NewStore(t)),
		NewComposer(defaultPersona()),
		NewDeterministicPlanner(defaultPersona()),
	)
	plan, err := planner.Plan(context.Background(), snapshot, sampleDecision())
	if err != nil {
		t.Fatalf("plan repair: %v", err)
	}
	if len(plan.PlannedActions) != 1 || plan.PlannedActions[0] != policydomain.ActionRepair {
		t.Fatalf("unexpected repair actions: %#v", plan.PlannedActions)
	}
}

func TestRecallableMessageIDsRejectOldAndForeignMessages(t *testing.T) {
	now := time.Now()
	snapshot := conversationdomain.ContextSnapshot{
		SelfID: 99,
		Event:  conversationdomain.ConversationEvent{TimestampUnix: now.Unix()},
		RecentTurns: []conversationdomain.ConversationEvent{
			{UserID: 99, MessageID: "old", Kind: conversationdomain.EventMessage, TimestampUnix: now.Add(-10 * time.Minute).Unix()},
			{UserID: 2, MessageID: "foreign", Kind: conversationdomain.EventMessage, TimestampUnix: now.Add(-time.Minute).Unix()},
			{UserID: 99, MessageID: "recalled", Kind: conversationdomain.EventMessage, TimestampUnix: now.Add(-time.Minute).Unix()},
			{UserID: 99, Kind: conversationdomain.EventRecall, ReplyToMessageID: "recalled", TimestampUnix: now.Add(-30 * time.Second).Unix()},
			{UserID: 99, MessageID: "recent", Kind: conversationdomain.EventMessage, TimestampUnix: now.Add(-time.Minute).Unix()},
		},
	}
	if got := recallableMessageIDs(snapshot); !slices.Equal(got, []string{"recent"}) {
		t.Fatalf("unexpected recallable messages: %#v", got)
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
