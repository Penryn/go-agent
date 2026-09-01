package prompting

import (
	"context"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/phlin/go-agent/internal/application/ports"
	"github.com/phlin/go-agent/internal/application/textutil"
	toolsvc "github.com/phlin/go-agent/internal/application/tools"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

type AgentPlanner struct {
	factory  ports.ChatModelFactory
	tools    *toolsvc.Runtime
	composer *Composer
	fallback *DeterministicPlanner
}

func NewAgentPlanner(factory ports.ChatModelFactory, tools *toolsvc.Runtime, composer *Composer, fallback *DeterministicPlanner) *AgentPlanner {
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

	toolContext := replydomain.ToolContext{
		TraceID:           snapshot.SnapshotID,
		GroupID:           snapshot.Event.GroupID,
		UserID:            snapshot.Event.UserID,
		TriggerMessageID:  snapshot.Event.MessageID,
		AllowedTools:      snapshot.GroupPolicy.ToolAllowlist,
		ObserveOnly:       false,
		RetrievedMemories: snapshot.RelevantMemories,
		MediaDescriptors:  snapshot.MediaDescriptors,
		Budget:            map[string]int{"update_affinity": 0, "update_member_profile": 0},
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

	returnDirectly := map[string]bool{
		"speak_text":  true,
		"quote_reply": true,
		"send_meme":   true,
		"react_emoji": true,
		"stay_silent": true,
	}
	// 只调用一次 Tools()，缓存结果供 agent 构建和日志共用
	toolList := p.tools.Tools(toolContext)

	slog.Info("planner: starting LLM agent",
		"trace_id", snapshot.SnapshotID,
		"group_id", snapshot.Event.GroupID,
		"user_id", snapshot.Event.UserID,
		"action", decision.Action,
		"tools", len(toolList),
	)

	maxIterations := defaultMaxIterations
	guard := newToolRuntimeGuard(snapshot.SnapshotID, defaultMaxToolCalls, defaultToolResultMaxBytes)

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "main_persona_agent",
		Description: "Generate a natural QQ group reply with controlled runtime tools.",
		Instruction: p.composer.Instruction(snapshot, decision),
		Model:       chatModel,
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
		assistantText string
		toolName      string
		toolContent   string
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
			toolName = event.Output.MessageOutput.ToolName
			toolContent = msg.Content
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

	if plan, ok, err := toolsvc.ParseTerminalPlan(decision.DecisionID, toolName, toolContent, toolContext); err == nil && ok {
		slog.Info("planner: terminal tool", "tool", toolName, "trace_id", snapshot.SnapshotID, "bubbles", len(plan.Bubbles))
		return plan, nil
	}

	if strings.TrimSpace(assistantText) != "" {
		slog.Info("planner: using raw assistant text", "trace_id", snapshot.SnapshotID)
		return replydomain.ReplyPlan{
			PlanID:         decision.DecisionID + "-plan",
			Intent:         toolContext.Intent,
			Bubbles:        []string{assistantText},
			PlannedActions: []policydomain.DecisionAction{policydomain.ActionReply},
			SendMode:       "group",
			FallbackText:   assistantText,
		}, nil
	}

	slog.Warn("planner: no output from agent, fallback", "trace_id", snapshot.SnapshotID)
	return p.fallback.Plan(ctx, snapshot, decision)
}
