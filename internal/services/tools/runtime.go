package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/phlin/go-agent/internal/core/ports"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

type Runtime struct {
	memoryStore  ports.MemoryStore
	memeStore    ports.MemeStore
	profileStore ports.ProfileStore
	searcher     ports.WebSearcher
}

type Option func(*Runtime)

func NewRuntime(memoryStore ports.MemoryStore, memeStore ports.MemeStore, opts ...Option) *Runtime {
	rt := &Runtime{
		memoryStore: memoryStore,
		memeStore:   memeStore,
	}
	for _, opt := range opts {
		opt(rt)
	}
	return rt
}

func WithProfileStore(store ports.ProfileStore) Option {
	return func(rt *Runtime) { rt.profileStore = store }
}

func WithWebSearcher(searcher ports.WebSearcher) Option {
	return func(rt *Runtime) { rt.searcher = searcher }
}

func (r *Runtime) Tools(session replydomain.ToolContext) []tool.BaseTool {
	all := []namedTool{
		newSpeakTextTool(),
		newStaySilentTool(),
		newQueryMemoryTool(r.memoryStore, session),
		newSearchMemeTool(r.memeStore, session),
		newSendMemeTool(),
		newQuoteReplyTool(),
		newQueryMemberProfileTool(r.profileStore, session),
		newWebSearchTool(r.searcher),
		newRecallRecentMessageTool(),
		newPokeMemberTool(),
		newMarkMemoryIntentTool(),
	}

	allowed := make([]tool.BaseTool, 0, len(all))
	for _, candidate := range all {
		if len(session.AllowedTools) == 0 || slices.Contains(session.AllowedTools, candidate.Name()) {
			allowed = append(allowed, candidate)
		}
	}
	return allowed
}

func ParseTerminalPlan(decisionID string, toolName string, raw string, session replydomain.ToolContext) (replydomain.ReplyPlan, bool, error) {
	switch toolName {
	case "speak_text":
		var result speakTextResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return replydomain.ReplyPlan{}, false, fmt.Errorf("decode speak_text result: %w", err)
		}
		bubbles := result.Bubbles
		if len(bubbles) == 0 && strings.TrimSpace(result.Text) != "" {
			bubbles = []string{result.Text}
		}
		return replydomain.ReplyPlan{
			PlanID:           decisionID + "-plan",
			Intent:           session.Intent,
			ReplyToMessageID: result.ReplyToMessageID,
			Bubbles:          bubbles,
			PlannedActions:   []policydomain.DecisionAction{policydomain.ActionReply},
			SendMode:         "group",
			FallbackText:     result.Text,
		}, true, nil
	case "stay_silent":
		return replydomain.ReplyPlan{
			PlanID:         decisionID + "-plan",
			Intent:         session.Intent,
			PlannedActions: []policydomain.DecisionAction{policydomain.ActionSilent},
			SendMode:       "group",
		}, true, nil
	case "quote_reply":
		var result quoteReplyResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return replydomain.ReplyPlan{}, false, fmt.Errorf("decode quote_reply result: %w", err)
		}
		return replydomain.ReplyPlan{
			PlanID:           decisionID + "-plan",
			Intent:           session.Intent,
			ReplyToMessageID: result.ReplyToMessageID,
			Bubbles:          compactStrings(result.Bubbles, 2),
			PlannedActions:   []policydomain.DecisionAction{policydomain.ActionReply},
			ActionParams:     map[string]any{"tool": "quote_reply"},
			SendMode:         "group",
			FallbackText:     result.Text,
		}, true, nil
	case "send_meme":
		var result sendMemeResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return replydomain.ReplyPlan{}, false, fmt.Errorf("decode send_meme result: %w", err)
		}
		return replydomain.ReplyPlan{
			PlanID:           decisionID + "-plan",
			Intent:           session.Intent,
			ReplyToMessageID: result.ReplyToMessageID,
			PlannedActions:   []policydomain.DecisionAction{policydomain.ActionMemeOnly},
			ActionParams: map[string]any{
				"meme_id":             result.MemeID,
				"reply_to_message_id": result.ReplyToMessageID,
				"caption":             result.Caption,
			},
			SendMode: "group",
		}, true, nil
	case "recall_recent_message":
		var result recallRecentMessageResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return replydomain.ReplyPlan{}, false, fmt.Errorf("decode recall_recent_message result: %w", err)
		}
		return replydomain.ReplyPlan{
			PlanID:         decisionID + "-plan",
			Intent:         session.Intent,
			PlannedActions: []policydomain.DecisionAction{policydomain.ActionRecall},
			ActionParams: map[string]any{
				"message_id": result.MessageID,
			},
			SendMode: "group",
		}, true, nil
	case "poke_member":
		var result pokeMemberResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return replydomain.ReplyPlan{}, false, fmt.Errorf("decode poke_member result: %w", err)
		}
		return replydomain.ReplyPlan{
			PlanID:         decisionID + "-plan",
			Intent:         session.Intent,
			PlannedActions: []policydomain.DecisionAction{policydomain.ActionPokeBack},
			ActionParams: map[string]any{
				"user_id": result.UserID,
			},
			SendMode: "group",
		}, true, nil
	default:
		return replydomain.ReplyPlan{}, false, nil
	}
}

type namedTool interface {
	tool.InvokableTool
	Name() string
}

type speakTextTool struct{}

type speakTextArgs struct {
	Text             string   `json:"text"`
	Bubbles          []string `json:"bubbles"`
	ReplyToMessageID string   `json:"reply_to_message_id"`
	MentionUserIDs   []int64  `json:"mention_user_ids"`
}

type speakTextResult struct {
	Tool             string   `json:"tool"`
	Text             string   `json:"text"`
	Bubbles          []string `json:"bubbles"`
	ReplyToMessageID string   `json:"reply_to_message_id"`
}

func newSpeakTextTool() *speakTextTool { return &speakTextTool{} }

func (t *speakTextTool) Name() string { return "speak_text" }

func (t *speakTextTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.Name(),
		Desc: "Send one or two short message bubbles as the bot's final reply in the current group.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"text":                {Type: schema.String, Required: true, Desc: "Primary reply text."},
			"bubbles":             {Type: schema.Array, Desc: "Optional split bubbles, at most two."},
			"reply_to_message_id": {Type: schema.String, Desc: "Optional message ID to quote-reply."},
		}),
	}, nil
}

func (t *speakTextTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args speakTextArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("decode speak_text args: %w", err)
	}
	if strings.TrimSpace(args.Text) == "" && len(args.Bubbles) == 0 {
		return "", errors.New("text or bubbles is required")
	}
	result := speakTextResult{
		Tool:             "speak_text",
		Text:             strings.TrimSpace(args.Text),
		Bubbles:          compactStrings(args.Bubbles, 2),
		ReplyToMessageID: args.ReplyToMessageID,
	}
	return marshal(result)
}

type staySilentTool struct{}

type staySilentArgs struct {
	ReasonCode string `json:"reason_code"`
	TTLMS      int    `json:"ttl_ms"`
}

func newStaySilentTool() *staySilentTool { return &staySilentTool{} }

func (t *staySilentTool) Name() string { return "stay_silent" }

func (t *staySilentTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.Name(),
		Desc: "Choose silence as the final action when replying would be unnatural or risky.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"reason_code": {Type: schema.String, Required: true, Desc: "Short reason code for staying silent."},
			"ttl_ms":      {Type: schema.Integer, Desc: "Optional suppression ttl in milliseconds."},
		}),
	}, nil
}

func (t *staySilentTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args staySilentArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("decode stay_silent args: %w", err)
	}
	return marshal(map[string]any{
		"tool":        "stay_silent",
		"reason_code": strings.TrimSpace(args.ReasonCode),
		"ttl_ms":      args.TTLMS,
	})
}

type queryMemoryTool struct {
	store   ports.MemoryStore
	session replydomain.ToolContext
}

type queryMemoryArgs struct {
	Query       string   `json:"query"`
	Scope       string   `json:"scope"`
	TopK        int      `json:"top_k"`
	MemoryTypes []string `json:"memory_types"`
}

func newQueryMemoryTool(store ports.MemoryStore, session replydomain.ToolContext) *queryMemoryTool {
	return &queryMemoryTool{store: store, session: session}
}

func (t *queryMemoryTool) Name() string { return "query_memory" }

func (t *queryMemoryTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.Name(),
		Desc: "Retrieve relevant approved long-term memories for the current group conversation.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"query":        {Type: schema.String, Required: true, Desc: "Memory lookup query."},
			"scope":        {Type: schema.String, Desc: "Optional memory scope."},
			"top_k":        {Type: schema.Integer, Desc: "Maximum number of records."},
			"memory_types": {Type: schema.Array, Desc: "Optional memory type filters."},
		}),
	}, nil
}

func (t *queryMemoryTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args queryMemoryArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("decode query_memory args: %w", err)
	}
	records, err := t.store.QueryMemories(ctx, ports.MemoryQuery{
		GroupID: t.session.GroupID,
		UserID:  t.session.UserID,
		Query:   args.Query,
		TopK:    clamp(args.TopK, 1, 5),
		Scope:   args.Scope,
		Types:   args.MemoryTypes,
	})
	if err != nil {
		return "", err
	}
	return marshal(map[string]any{"records": records})
}

type searchMemeTool struct {
	store   ports.MemeStore
	session replydomain.ToolContext
}

type searchMemeArgs struct {
	Query         string `json:"query"`
	Emotion       string `json:"emotion"`
	Scene         string `json:"scene"`
	TopK          int    `json:"top_k"`
	ExcludeRecent bool   `json:"exclude_recent"`
}

func newSearchMemeTool(store ports.MemeStore, session replydomain.ToolContext) *searchMemeTool {
	return &searchMemeTool{store: store, session: session}
}

func (t *searchMemeTool) Name() string { return "search_meme" }

func (t *searchMemeTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.Name(),
		Desc: "Search approved meme assets for the current group by keywords and mood.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"query":          {Type: schema.String, Required: true, Desc: "Keyword query for meme search."},
			"emotion":        {Type: schema.String, Desc: "Optional emotion constraint."},
			"scene":          {Type: schema.String, Desc: "Optional scene constraint."},
			"top_k":          {Type: schema.Integer, Desc: "Maximum number of results."},
			"exclude_recent": {Type: schema.Boolean, Desc: "Whether to exclude recently used memes."},
		}),
	}, nil
}

func (t *searchMemeTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args searchMemeArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("decode search_meme args: %w", err)
	}
	results, err := t.store.SearchMemes(ctx, ports.MemeQuery{
		GroupID:       t.session.GroupID,
		Query:         args.Query,
		Emotion:       args.Emotion,
		Scene:         args.Scene,
		TopK:          clamp(args.TopK, 1, 5),
		ExcludeRecent: args.ExcludeRecent,
	})
	if err != nil {
		return "", err
	}
	return marshal(map[string]any{"results": results})
}

type sendMemeTool struct{}

type sendMemeArgs struct {
	MemeID           string `json:"meme_id"`
	ReplyToMessageID string `json:"reply_to_message_id"`
	Caption          string `json:"caption"`
}

type sendMemeResult struct {
	Tool             string `json:"tool"`
	MemeID           string `json:"meme_id"`
	ReplyToMessageID string `json:"reply_to_message_id"`
	Caption          string `json:"caption"`
}

func newSendMemeTool() *sendMemeTool { return &sendMemeTool{} }
func (t *sendMemeTool) Name() string { return "send_meme" }
func (t *sendMemeTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.Name(),
		Desc: "Send an approved meme asset as the final group action.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"meme_id":             {Type: schema.String, Required: true, Desc: "Approved meme ID."},
			"reply_to_message_id": {Type: schema.String, Desc: "Optional message ID to quote reply."},
			"caption":             {Type: schema.String, Desc: "Optional short caption."},
		}),
	}, nil
}
func (t *sendMemeTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args sendMemeArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", err
	}
	return marshal(sendMemeResult{Tool: t.Name(), MemeID: args.MemeID, ReplyToMessageID: args.ReplyToMessageID, Caption: args.Caption})
}

type quoteReplyTool struct{}
type quoteReplyArgs struct {
	ReplyToMessageID string   `json:"reply_to_message_id"`
	Text             string   `json:"text"`
	Bubbles          []string `json:"bubbles"`
}
type quoteReplyResult struct {
	Tool             string   `json:"tool"`
	ReplyToMessageID string   `json:"reply_to_message_id"`
	Text             string   `json:"text"`
	Bubbles          []string `json:"bubbles"`
}

func newQuoteReplyTool() *quoteReplyTool { return &quoteReplyTool{} }
func (t *quoteReplyTool) Name() string   { return "quote_reply" }
func (t *quoteReplyTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.Name(),
		Desc: "Send a text reply while quoting a specific message.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"reply_to_message_id": {Type: schema.String, Required: true, Desc: "Message ID to quote."},
			"text":                {Type: schema.String, Required: true, Desc: "Reply text."},
			"bubbles":             {Type: schema.Array, Desc: "Optional split bubbles."},
		}),
	}, nil
}
func (t *quoteReplyTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args quoteReplyArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", err
	}
	return marshal(quoteReplyResult{Tool: t.Name(), ReplyToMessageID: args.ReplyToMessageID, Text: strings.TrimSpace(args.Text), Bubbles: compactStrings(args.Bubbles, 2)})
}

type queryMemberProfileTool struct {
	store   ports.ProfileStore
	session replydomain.ToolContext
}

type queryMemberProfileArgs struct {
	UserID int64    `json:"user_id"`
	Fields []string `json:"fields"`
}

func newQueryMemberProfileTool(store ports.ProfileStore, session replydomain.ToolContext) *queryMemberProfileTool {
	return &queryMemberProfileTool{store: store, session: session}
}
func (t *queryMemberProfileTool) Name() string { return "query_member_profile" }
func (t *queryMemberProfileTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.Name(),
		Desc: "Read the current group member profile and relationship state.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"user_id": {Type: schema.Integer, Required: true, Desc: "Target member user ID."},
			"fields":  {Type: schema.Array, Desc: "Optional requested fields."},
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

type webSearchTool struct {
	searcher ports.WebSearcher
}

type webSearchArgs struct {
	Query     string `json:"query"`
	TopK      int    `json:"top_k"`
	Freshness string `json:"freshness"`
}

func newWebSearchTool(searcher ports.WebSearcher) *webSearchTool {
	return &webSearchTool{searcher: searcher}
}
func (t *webSearchTool) Name() string { return "web_search" }
func (t *webSearchTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.Name(),
		Desc: "Search the web and return concise citations.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"query":     {Type: schema.String, Required: true, Desc: "Search query."},
			"top_k":     {Type: schema.Integer, Desc: "Max number of results."},
			"freshness": {Type: schema.String, Desc: "Optional freshness hint."},
		}),
	}, nil
}
func (t *webSearchTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args webSearchArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", err
	}
	if t.searcher == nil {
		return marshal(map[string]any{"results": []ports.SearchResult{}})
	}
	results, err := t.searcher.Search(ctx, args.Query, clamp(args.TopK, 1, 5), args.Freshness)
	if err != nil {
		return "", err
	}
	return marshal(map[string]any{"results": results})
}

type recallRecentMessageTool struct{}
type recallRecentMessageArgs struct {
	MessageID  string `json:"message_id"`
	ReasonCode string `json:"reason_code"`
}
type recallRecentMessageResult struct {
	Tool      string `json:"tool"`
	MessageID string `json:"message_id"`
}

func newRecallRecentMessageTool() *recallRecentMessageTool { return &recallRecentMessageTool{} }
func (t *recallRecentMessageTool) Name() string            { return "recall_recent_message" }
func (t *recallRecentMessageTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.Name(),
		Desc: "Recall a recent bot message through the runtime executor.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"message_id":  {Type: schema.String, Required: true, Desc: "Target bot message ID."},
			"reason_code": {Type: schema.String, Desc: "Optional reason."},
		}),
	}, nil
}
func (t *recallRecentMessageTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args recallRecentMessageArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", err
	}
	return marshal(recallRecentMessageResult{Tool: t.Name(), MessageID: args.MessageID})
}

type pokeMemberTool struct{}
type pokeMemberArgs struct {
	UserID     int64  `json:"user_id"`
	ReasonCode string `json:"reason_code"`
}
type pokeMemberResult struct {
	Tool   string `json:"tool"`
	UserID int64  `json:"user_id"`
}

func newPokeMemberTool() *pokeMemberTool { return &pokeMemberTool{} }
func (t *pokeMemberTool) Name() string   { return "poke_member" }
func (t *pokeMemberTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.Name(),
		Desc: "Poke a group member when the platform policy allows it.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"user_id":     {Type: schema.Integer, Required: true, Desc: "Target member user ID."},
			"reason_code": {Type: schema.String, Desc: "Optional reason."},
		}),
	}, nil
}
func (t *pokeMemberTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args pokeMemberArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", err
	}
	return marshal(pokeMemberResult{Tool: t.Name(), UserID: args.UserID})
}

type markMemoryIntentTool struct{}

type markMemoryIntentArgs struct {
	MemoryType      string  `json:"memory_type"`
	Subject         string  `json:"subject"`
	Content         string  `json:"content"`
	Importance      float64 `json:"importance"`
	EvidenceEventID string  `json:"evidence_event_id"`
}

func newMarkMemoryIntentTool() *markMemoryIntentTool { return &markMemoryIntentTool{} }

func (t *markMemoryIntentTool) Name() string { return "mark_memory_intent" }

func (t *markMemoryIntentTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.Name(),
		Desc: "Submit a structured memory-write intent for later validation and persistence.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"memory_type":       {Type: schema.String, Required: true, Desc: "Memory type."},
			"subject":           {Type: schema.String, Required: true, Desc: "Memory subject."},
			"content":           {Type: schema.String, Required: true, Desc: "Memory content."},
			"importance":        {Type: schema.Number, Desc: "Importance score from 0 to 1."},
			"evidence_event_id": {Type: schema.String, Desc: "Supporting event ID."},
		}),
	}, nil
}

func (t *markMemoryIntentTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args markMemoryIntentArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("decode mark_memory_intent args: %w", err)
	}
	intentID := fmt.Sprintf("memory-intent-%d", time.Now().UnixNano())
	return marshal(map[string]any{
		"accepted":         true,
		"memory_intent_id": intentID,
		"memory_type":      args.MemoryType,
		"subject":          args.Subject,
	})
}

func marshal(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func compactStrings(values []string, max int) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result = append(result, value)
		}
		if max > 0 && len(result) == max {
			break
		}
	}
	return result
}

func clamp(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
