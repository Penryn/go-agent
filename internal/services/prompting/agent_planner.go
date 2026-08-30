package prompting

import (
	"context"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	"github.com/phlin/go-agent/internal/services/textutil"
	toolsvc "github.com/phlin/go-agent/internal/services/tools"
)

type AgentPlanner struct {
	factory        ports.ChatModelFactory
	tools          *toolsvc.Runtime
	composer       *Composer
	fallback       *DeterministicPlanner
	maxIterations  int
	maxToolCalls   int
	maxResultBytes int
	toolAuditHook  ToolAuditHook
}

func NewAgentPlanner(factory ports.ChatModelFactory, tools *toolsvc.Runtime, composer *Composer, fallback *DeterministicPlanner) *AgentPlanner {
	return &AgentPlanner{
		factory:        factory,
		tools:          tools,
		composer:       composer,
		fallback:       fallback,
		maxIterations:  defaultMaxIterations,
		maxToolCalls:   defaultMaxToolCalls,
		maxResultBytes: defaultToolResultMaxBytes,
	}
}

// SetToolRuntimeLimits changes the per-plan tool loop safety limits. Values
// less than one leave the corresponding default unchanged. It is intended for
// process configuration and should be called before serving traffic.
func (p *AgentPlanner) SetToolRuntimeLimits(maxIterations, maxToolCalls, maxResultBytes int) {
	if maxIterations > 0 {
		p.maxIterations = maxIterations
	}
	if maxToolCalls > 0 {
		p.maxToolCalls = maxToolCalls
	}
	if maxResultBytes > 0 {
		p.maxResultBytes = maxResultBytes
	}
}

// SetToolAuditHook installs a callback for tool execution events. The hook is
// best-effort: panics are recovered by the guard so observability never breaks
// a user-facing reply.
func (p *AgentPlanner) SetToolAuditHook(hook ToolAuditHook) {
	p.toolAuditHook = hook
}

func (p *AgentPlanner) Plan(ctx context.Context, snapshot conversationdomain.ContextSnapshot, decision policydomain.AutonomyDecision) (replydomain.ReplyPlan, error) {
	observeOnly := decision.Action == policydomain.ActionSilent

	if observeOnly {
		// 没有实质内容时跳过 LLM，无观测价值
		hasContent := strings.TrimSpace(snapshot.Event.Text) != "" || len(snapshot.Event.Attachments) > 0
		if !hasContent {
			slog.Debug("planner: action silent, no content to observe", "trace_id", snapshot.SnapshotID)
			return p.fallback.Plan(ctx, snapshot, decision)
		}
	}

	// ActionPokeBack 不需要 LLM，直接走 fallback（fallback 已有 ActionPokeBack 分支）
	if decision.Action == policydomain.ActionPokeBack {
		return p.fallback.Plan(ctx, snapshot, decision)
	}
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
		AllowedTools:      snapshot.GroupPolicy.ToolAllowlist,
		ObserveOnly:       observeOnly,
		RetrievedMemories: snapshot.RelevantMemories,
		MediaDescriptors:  snapshot.MediaDescriptors,
		Budget:            map[string]int{"update_affinity": 0, "update_member_profile": 0},
		Intent: replydomain.ReplyIntent{
			Kind:            "chat",
			Goal:            "自然接话",
			TargetUserIDs:   []int64{snapshot.Event.UserID},
			PreferMeme:      p.fallback.persona.PreferMemes,
			PreferShortText: true,
			MaxChars:        p.fallback.persona.ReplyMaxChars,
		},
	}

	returnDirectly := map[string]bool{
		"speak_text":  true,
		"quote_reply": true,
		"stay_silent": true,
	}
	if observeOnly {
		returnDirectly = map[string]bool{"stay_silent": true}
	}

	// 只调用一次 Tools()，缓存结果供 agent 构建和日志共用
	toolList := p.tools.Tools(toolContext)

	slog.Info("planner: starting LLM agent",
		"trace_id", snapshot.SnapshotID,
		"group_id", snapshot.Event.GroupID,
		"user_id", snapshot.Event.UserID,
		"action", decision.Action,
		"observe_only", observeOnly,
		"tools", len(toolList),
	)

	// observe-only 只需 query+mark 两步，不需要完整的 4 轮规划
	maxIterations := p.maxIterations
	if maxIterations <= 0 {
		maxIterations = defaultMaxIterations
	}
	if observeOnly {
		maxIterations = minInt(maxIterations, 2)
	}
	maxToolCalls := p.maxToolCalls
	if maxToolCalls <= 0 {
		maxToolCalls = defaultMaxToolCalls
	}
	maxResultBytes := p.maxResultBytes
	if maxResultBytes <= 0 {
		maxResultBytes = defaultToolResultMaxBytes
	}
	guard := newToolRuntimeGuard(snapshot.SnapshotID, maxToolCalls, maxResultBytes, p.toolAuditHook)

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

	// observe-only 模式：LLM 只用于被动观测，无论输出什么都保持沉默
	if observeOnly {
		slog.Info("planner: observe-only complete, staying silent", "trace_id", snapshot.SnapshotID)
		return replydomain.ReplyPlan{
			PlanID:         decision.DecisionID + "-plan",
			PlannedActions: []policydomain.DecisionAction{policydomain.ActionSilent},
			SendMode:       "group",
		}, nil
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
