package prompting

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	modelcomponent "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/phlin/go-agent/internal/application/modelusage"
	"github.com/phlin/go-agent/internal/application/textutil"
	toolsvc "github.com/phlin/go-agent/internal/application/tools"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

// MainModelFactory 是本包对模型工厂的最小依赖面(接口定义在消费方)。
type MainModelFactory interface {
	MainChatModel(ctx context.Context) (modelcomponent.BaseChatModel, error)
}

type AgentPlanner struct {
	factory  MainModelFactory
	tools    *toolsvc.Runtime
	composer *Composer
	fallback *DeterministicPlanner
}

func NewAgentPlanner(factory MainModelFactory, tools *toolsvc.Runtime, composer *Composer, fallback *DeterministicPlanner) *AgentPlanner {
	return &AgentPlanner{
		factory:  factory,
		tools:    tools,
		composer: composer,
		fallback: fallback,
	}
}

func (p *AgentPlanner) Plan(ctx context.Context, snapshot conversationdomain.ContextSnapshot, decision policydomain.AutonomyDecision) (replydomain.ReplyPlan, error) {
	maxChars, _ := replyBudget(p.fallback.persona, snapshot, decision.TriggerType)
	if p.tools == nil {
		slog.Warn("planner: no tool runtime, fallback", "trace_id", snapshot.SnapshotID)
		return p.fallback.Plan(ctx, snapshot, decision)
	}

	chatModel, err := p.factory.MainChatModel(ctx)
	if err != nil || chatModel == nil {
		slog.Warn("planner: no chat model, fallback", "trace_id", snapshot.SnapshotID, "error", err)
		return p.fallback.Plan(ctx, snapshot, decision)
	}
	chatModel = modelusage.Wrap(chatModel)

	toolContext := replydomain.ToolContext{
		TraceID:              snapshot.SnapshotID,
		GroupID:              snapshot.Event.GroupID,
		UserID:               snapshot.Event.UserID,
		TriggerMessageID:     snapshot.Event.MessageID,
		TriggerEventID:       snapshot.Event.EventID,
		TriggerTimestampUnix: snapshot.Event.TimestampUnix,
		AllowedTools:         snapshot.GroupPolicy.ToolAllowlist,
		RetrievedMemories:    snapshot.RelevantMemories,
		MediaDescriptors:     snapshot.MediaDescriptors,
		Budget:               map[string]int{"update_affinity": 0, "update_member_profile": 0, "update_persona_fact": 0},
		TriggerType:          decision.TriggerType,
		RecallableMessageIDs: recallableMessageIDs(snapshot),
		Intent: replydomain.ReplyIntent{
			Kind:            "chat",
			Goal:            dialogueGoal(decision.TriggerType),
			TargetUserIDs:   []int64{snapshot.Event.UserID},
			PreferMeme:      p.fallback.persona.PreferMemes,
			PreferShortText: true,
			MaxChars:        maxChars,
		},
	}
	ctx = p.tools.ToolContext(ctx, toolContext.GroupID, toolContext.UserID)

	// 只调用一次 Tools()，缓存结果供 agent 构建和日志共用
	toolList := p.tools.Tools(toolContext)
	returnDirectly := p.tools.TerminalTools(toolContext)

	slog.Info("planner: starting LLM agent",
		"trace_id", snapshot.SnapshotID,
		"group_id", snapshot.Event.GroupID,
		"user_id", snapshot.Event.UserID,
		"action", decision.Action,
		"tools", len(toolList),
	)

	maxIterations := defaultMaxIterations
	guard := newToolRuntimeGuard(snapshot.SnapshotID, defaultMaxToolCalls, defaultToolResultMaxBytes, returnDirectly)

	staticInstruction := p.composer.StaticInstruction()
	dynamicInstruction := p.composer.DynamicInstruction(snapshot, decision)
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "main_persona_agent",
		Description: "Generate a natural QQ group reply with controlled runtime tools.",
		Instruction: staticInstruction,
		GenModelInput: func(_ context.Context, instruction string, input *adk.AgentInput) ([]*schema.Message, error) {
			messages := make([]*schema.Message, 0, len(input.Messages)+2)
			if strings.TrimSpace(instruction) != "" {
				messages = append(messages, schema.SystemMessage(instruction))
			}
			if strings.TrimSpace(dynamicInstruction) != "" {
				messages = append(messages, schema.SystemMessage(dynamicInstruction))
			}
			messages = append(messages, input.Messages...)
			return messages, nil
		},
		Model: chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: toolList,
				ToolCallMiddlewares: []compose.ToolMiddleware{{
					Invokable: guard.middleware,
				}},
			},
			ReturnDirectly: returnDirectly,
		},
		MaxIterations: maxIterations,
	})
	if err != nil {
		slog.Warn("planner: create agent failed, fallback", "trace_id", snapshot.SnapshotID, "error", err)
		return p.fallback.Plan(ctx, snapshot, decision)
	}

	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})
	iter := runner.Run(ctx, p.composer.Messages(snapshot))

	var (
		assistantText   string
		terminalName    string
		terminalContent string
	)
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event == nil || event.Err != nil || event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}

		msg, err := event.Output.MessageOutput.GetMessage()
		if err != nil || msg == nil {
			continue
		}

		if event.Output.MessageOutput.Role == schema.Tool {
			toolName := event.Output.MessageOutput.ToolName
			if returnDirectly[toolName] && terminalName == "" && !toolResultFailed(msg.Content) {
				terminalName = toolName
				terminalContent = msg.Content
			}
			slog.Debug("planner: tool result", "tool", toolName, "trace_id", snapshot.SnapshotID)
			continue
		}

		if event.Output.MessageOutput.Role == schema.Assistant && strings.TrimSpace(msg.Content) != "" {
			cleaned := textutil.StripThinkBlocks(msg.Content)
			if strings.TrimSpace(cleaned) == "" {
				continue
			}
			assistantText = cleaned
			preview := assistantText
			if len(preview) > 80 {
				preview = preview[:80] + "..."
			}
			slog.Debug("planner: assistant output", "trace_id", snapshot.SnapshotID, "text", preview)
		}
	}

	if plan, ok, err := toolsvc.ParseTerminalPlan(decision.DecisionID, terminalName, terminalContent, toolContext); err == nil && ok {
		slog.Info("planner: terminal tool", "tool", terminalName, "trace_id", snapshot.SnapshotID, "bubbles", len(plan.Bubbles))
		return plan, nil
	}

	if strings.TrimSpace(assistantText) != "" {
		slog.Info("planner: using raw assistant text", "trace_id", snapshot.SnapshotID)
		bubbles := textutil.SplitNaturalBubbles(assistantText, 2)
		return replydomain.ReplyPlan{
			PlanID:         decision.DecisionID + "-plan",
			Intent:         toolContext.Intent,
			Bubbles:        bubbles,
			PlannedActions: []policydomain.DecisionAction{policydomain.ActionReply},
			SendMode:       "group",
			FallbackText:   assistantText,
		}, nil
	}

	slog.Warn("planner: no output from agent, fallback", "trace_id", snapshot.SnapshotID)
	return p.fallback.Plan(ctx, snapshot, decision)
}

const (
	recallWindow          = 5 * time.Minute
	maxRecallableMessages = 3
)

func recallableMessageIDs(snapshot conversationdomain.ContextSnapshot) []string {
	if snapshot.SelfID == 0 {
		return nil
	}
	base := time.Now()
	if snapshot.Event.TimestampUnix > 0 {
		base = time.Unix(snapshot.Event.TimestampUnix, 0)
	}
	result := make([]string, 0, maxRecallableMessages)
	recalled := make(map[string]bool)
	for i := len(snapshot.RecentTurns) - 1; i >= 0 && len(result) < maxRecallableMessages; i-- {
		turn := snapshot.RecentTurns[i]
		if turn.UserID == snapshot.SelfID && turn.Kind == conversationdomain.EventRecall && turn.ReplyToMessageID != "" {
			recalled[turn.ReplyToMessageID] = true
			continue
		}
		if turn.UserID != snapshot.SelfID || turn.MessageID == "" || turn.Kind != conversationdomain.EventMessage {
			continue
		}
		if recalled[turn.MessageID] {
			continue
		}
		if turn.TimestampUnix > 0 {
			age := base.Sub(time.Unix(turn.TimestampUnix, 0))
			if age < 0 || age > recallWindow {
				continue
			}
		}
		result = append(result, turn.MessageID)
	}
	return result
}

func toolResultFailed(raw string) bool {
	var envelope struct {
		Error string `json:"error"`
	}
	return json.Unmarshal([]byte(raw), &envelope) == nil && envelope.Error != ""
}
