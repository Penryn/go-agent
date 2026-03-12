package prompting

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	toolsvc "github.com/phlin/go-agent/internal/services/tools"
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
	if decision.Action == policydomain.ActionSilent {
		return p.fallback.Plan(ctx, snapshot, decision)
	}

	chatModel, err := p.factory.MainChatModel(ctx)
	if err != nil || chatModel == nil {
		return p.fallback.Plan(ctx, snapshot, decision)
	}

	toolContext := replydomain.ToolContext{
		TraceID:           snapshot.SnapshotID,
		GroupID:           snapshot.Event.GroupID,
		UserID:            snapshot.Event.UserID,
		AllowedTools:      snapshot.GroupPolicy.ToolAllowlist,
		RetrievedMemories: snapshot.RelevantMemories,
		MediaDescriptors:  snapshot.MediaDescriptors,
		Intent: replydomain.ReplyIntent{
			Kind:            "chat",
			Goal:            "自然接话",
			TargetUserIDs:   []int64{snapshot.Event.UserID},
			PreferShortText: true,
			MaxChars:        p.fallback.persona.ReplyMaxChars,
		},
	}

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "main_persona_agent",
		Description: "Generate a natural QQ group reply with controlled runtime tools.",
		Instruction: p.composer.Instruction(snapshot, decision),
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: p.tools.Tools(toolContext),
			},
			ReturnDirectly: map[string]bool{
				"speak_text":  true,
				"stay_silent": true,
			},
		},
		MaxIterations: 4,
	})
	if err != nil {
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
			continue
		}

		if event.Output.MessageOutput.Role == schema.Assistant && strings.TrimSpace(msg.Content) != "" {
			assistantText = msg.Content
		}
	}

	if plan, ok, err := toolsvc.ParseTerminalPlan(decision.DecisionID, toolName, toolContent, toolContext); err == nil && ok {
		if plan.ReplyToMessageID == "" {
			plan.ReplyToMessageID = snapshot.Event.MessageID
		}
		return plan, nil
	}

	if strings.TrimSpace(assistantText) != "" {
		return replydomain.ReplyPlan{
			PlanID:           decision.DecisionID + "-plan",
			Intent:           toolContext.Intent,
			ReplyToMessageID: snapshot.Event.MessageID,
			Bubbles:          []string{assistantText},
			PlannedActions:   []policydomain.DecisionAction{policydomain.ActionReply},
			SendMode:         "group",
			FallbackText:     assistantText,
		}, nil
	}

	return p.fallback.Plan(ctx, snapshot, decision)
}
