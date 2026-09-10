package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	retrievalsvc "github.com/phlin/go-agent/internal/application/retrieval"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	"github.com/phlin/go-agent/internal/application/ports"
)

// query_memory 工具 - 查询记忆
type queryMemoryTool struct {
	retriever *retrievalsvc.Service
	session   replydomain.ToolContext
}

func newQueryMemoryTool(retriever *retrievalsvc.Service, session replydomain.ToolContext) *queryMemoryTool {
	return &queryMemoryTool{retriever: retriever, session: session}
}

func (t *queryMemoryTool) Name() string { return "query_memory" }

func (t *queryMemoryTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "query_memory",
		Desc: "Search conversation history and knowledge base",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"query":        {Type: "string", Desc: "Search query", Required: true},
			"scope":        {Type: "string", Desc: "Search scope: all/conversation/knowledge"},
			"top_k":        {Type: "integer", Desc: "Maximum results to return"},
			"memory_types": {Type: "array", Desc: "Types of memory to search"},
		}),
	}, nil
}

func (t *queryMemoryTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args queryMemoryArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("decode query_memory args: %w", err)
	}
	slog.Debug("tool: query_memory", "query", args.Query, "scope", args.Scope, "top_k", args.TopK)

	query := ports.MemoryQuery{
		GroupID: t.session.GroupID,
		UserID:  t.session.UserID,
		Query:   args.Query,
		TopK:    clamp(args.TopK, 1, 5),
		Scope:   args.Scope,
		Types:   args.MemoryTypes,
		TraceID: t.session.TraceID,
		EventID: t.session.TriggerEventID,
	}
	if t.retriever == nil {
		return marshal(map[string]any{"records": nil})
	}
	records, err := t.retriever.SearchMemories(ctx, query)
	if err != nil {
		return "", err
	}
	slog.Debug("tool: query_memory result", "count", len(records))
	return marshal(map[string]any{"records": records})
}

// query_member_profile 工具 - 查询成员资料
type queryMemberProfileTool struct {
	store   ports.ProfileStore
	session replydomain.ToolContext
}

func newQueryMemberProfileTool(store ports.ProfileStore, session replydomain.ToolContext) *queryMemberProfileTool {
	return &queryMemberProfileTool{store: store, session: session}
}

func (t *queryMemberProfileTool) Name() string { return "query_member_profile" }

func (t *queryMemberProfileTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "query_member_profile",
		Desc: "Get information about a group member",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"user_id": {Type: "integer", Desc: "User ID to query", Required: true},
			"fields":  {Type: "array", Desc: "Specific fields to retrieve"},
		}),
	}, nil
}

func (t *queryMemberProfileTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	if t.store == nil {
		return marshal(map[string]any{"profile": nil})
	}
	var args queryMemberProfileArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", err
	}
	profile, err := t.store.GetMemberProfile(ctx, t.session.GroupID, args.UserID)
	if err != nil {
		return "", err
	}
	return marshal(map[string]any{"profile": profile})
}

// clamp 限制值在指定范围内
func clamp(value, minValue, maxValue int) int {
	return min(max(value, minValue), maxValue)
}
