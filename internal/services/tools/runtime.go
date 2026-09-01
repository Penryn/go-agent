package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/phlin/go-agent/internal/core/ports"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	memesvc "github.com/phlin/go-agent/internal/services/meme"
	memsvc "github.com/phlin/go-agent/internal/services/memory"
	retrievalsvc "github.com/phlin/go-agent/internal/services/retrieval"
	"github.com/phlin/go-agent/internal/services/textutil"
)

type Runtime struct {
	memoryStore  ports.MemoryStore
	memeStore    ports.MemeStore
	profileStore ports.ProfileStore
	personaID    string
	memSvc       *memsvc.Service
	memeSvc      *memesvc.Service
	retriever    *retrievalsvc.Service
	external     []registeredTool
	approvals    *WriteApprovalStore
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

func WithPersonaID(id string) Option {
	return func(rt *Runtime) { rt.personaID = id }
}

func WithMemoryService(svc *memsvc.Service) Option {
	return func(rt *Runtime) { rt.memSvc = svc }
}

func WithMemeService(svc *memesvc.Service) Option {
	return func(rt *Runtime) { rt.memeSvc = svc }
}

func WithMemoryRetriever(retriever *retrievalsvc.Service) Option {
	return func(rt *Runtime) { rt.retriever = retriever }
}

func WithWriteApprovalStore(store *WriteApprovalStore) Option {
	return func(rt *Runtime) { rt.approvals = store }
}

func (r *Runtime) ObserveConfirmation(groupID, userID int64, text string, at time.Time) {
	if r != nil && r.approvals != nil {
		r.approvals.ObserveConfirmation(groupID, userID, text, at)
	}
}

func (r *Runtime) ToolContext(ctx context.Context, groupID, userID int64) context.Context {
	return withToolIdentity(ctx, groupID, userID)
}

// speakingTools 是在 observe-only 模式下需要排除的发言/行动工具。
var speakingTools = map[string]bool{
	"speak_text":            true,
	"quote_reply":           true,
	"send_meme":             true,
	"recall_recent_message": true,
	"poke_member":           true,
}

func (r *Runtime) Tools(session replydomain.ToolContext) []tool.BaseTool {
	internal := make([]namedTool, 0, 13)
	internal = append(internal, r.replyTools()...)
	internal = append(internal, r.knowledgeTools(session)...)
	internal = append(internal, r.profileTools(session)...)
	all := make([]registeredTool, 0, len(internal)+len(r.external))
	for _, candidate := range internal {
		all = append(all, registeredTool{name: candidate.Name(), tool: candidate})
	}
	all = append(all, r.external...)

	allowed := make([]tool.BaseTool, 0, len(all))
	for _, candidate := range all {
		if session.ObserveOnly && speakingTools[candidate.name] {
			continue
		}
		if (candidate.external && !slices.Contains(session.AllowedTools, candidate.name)) ||
			(!candidate.external && len(session.AllowedTools) > 0 && !slices.Contains(session.AllowedTools, candidate.name)) {
			continue
		}
		allowed = append(allowed, candidate.tool)
	}
	return allowed
}

type registeredTool struct {
	name     string
	tool     tool.BaseTool
	external bool
}

// RegisterTools adds tools discovered at startup (for example MCP and Codex)
// while preserving the existing per-group allowlist behavior.
func (r *Runtime) RegisterTools(ctx context.Context, tools ...tool.BaseTool) error {
	known := make(map[string]bool, len(r.external)+13)
	for _, candidate := range r.replyTools() {
		known[candidate.Name()] = true
	}
	for _, candidate := range r.knowledgeTools(replydomain.ToolContext{}) {
		known[candidate.Name()] = true
	}
	for _, candidate := range r.profileTools(replydomain.ToolContext{}) {
		known[candidate.Name()] = true
	}
	for _, candidate := range r.external {
		known[candidate.name] = true
	}
	for _, candidate := range tools {
		info, err := candidate.Info(ctx)
		if err != nil {
			return fmt.Errorf("read external tool info: %w", err)
		}
		if info == nil || strings.TrimSpace(info.Name) == "" {
			return errors.New("external tool name is required")
		}
		if known[info.Name] {
			return fmt.Errorf("duplicate tool name %q", info.Name)
		}
		known[info.Name] = true
		r.external = append(r.external, registeredTool{name: info.Name, tool: candidate, external: true})
	}
	return nil
}

func (r *Runtime) replyTools() []namedTool {
	return []namedTool{
		newSpeakTextTool(),
		newStaySilentTool(),
		newReactEmojiTool(),
		newSendMemeTool(r.memeStore),
		newQuoteReplyTool(),
		newRecallRecentMessageTool(),
		newPokeMemberTool(),
	}
}

func (r *Runtime) knowledgeTools(session replydomain.ToolContext) []namedTool {
	return []namedTool{
		newQueryMemoryTool(r.memoryStore, r.retriever, session),
		newSearchMemeTool(r.memeStore, r.memeSvc, session),
	}
}

func (r *Runtime) profileTools(session replydomain.ToolContext) []namedTool {
	return []namedTool{
		newQueryMemberProfileTool(r.profileStore, session),
		newMarkMemoryIntentTool(r.memSvc, session),
		newUpdateAffinityTool(r.profileStore, session, r.personaID),
		newUpdateMemberProfileTool(r.profileStore, session, r.personaID),
	}
}

func ParseTerminalPlan(decisionID string, toolName string, raw string, session replydomain.ToolContext) (replydomain.ReplyPlan, bool, error) {
	switch toolName {
	case "speak_text":
		var result speakTextResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return replydomain.ReplyPlan{}, false, fmt.Errorf("decode speak_text result: %w", err)
		}
		cleanedText := textutil.StripThinkBlocks(result.Text)
		cleanedBubbles := make([]string, 0, len(result.Bubbles))
		for _, b := range result.Bubbles {
			cleanedBubbles = append(cleanedBubbles, textutil.StripThinkBlocks(b))
		}
		bubbles := cleanedBubbles
		if len(bubbles) == 0 && strings.TrimSpace(cleanedText) != "" {
			bubbles = []string{cleanedText}
		}
		return replydomain.ReplyPlan{
			PlanID:           decisionID + "-plan",
			Intent:           session.Intent,
			ReplyToMessageID: result.ReplyToMessageID,
			Bubbles:          bubbles,
			PlannedActions:   []policydomain.DecisionAction{policydomain.ActionReply},
			SendMode:         "group",
			FallbackText:     cleanedText,
		}, true, nil
	case "stay_silent":
		return replydomain.ReplyPlan{
			PlanID:         decisionID + "-plan",
			Intent:         session.Intent,
			PlannedActions: []policydomain.DecisionAction{policydomain.ActionSilent},
			SendMode:       "group",
		}, true, nil
	case "react_emoji":
		var result reactEmojiResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return replydomain.ReplyPlan{}, false, fmt.Errorf("decode react_emoji result: %w", err)
		}
		messageID := result.MessageID
		if messageID == "" {
			messageID = session.TriggerMessageID
		}
		return replydomain.ReplyPlan{
			PlanID:         decisionID + "-plan",
			Intent:         session.Intent,
			PlannedActions: []policydomain.DecisionAction{policydomain.ActionReact},
			ActionParams: map[string]any{
				"emoji_id":   result.EmojiID,
				"message_id": messageID,
			},
			SendMode: "group",
		}, true, nil
	case "quote_reply":
		var result quoteReplyResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return replydomain.ReplyPlan{}, false, fmt.Errorf("decode quote_reply result: %w", err)
		}
		cleanedText := textutil.StripThinkBlocks(result.Text)
		rawBubbles := compactStrings(result.Bubbles, 2)
		cleanedBubbles := make([]string, 0, len(rawBubbles))
		for _, b := range rawBubbles {
			cleanedBubbles = append(cleanedBubbles, textutil.StripThinkBlocks(b))
		}
		if result.ReplyToMessageID == "" {
			// LLM 未提供引用 ID，降级为普通发言，不设置 ActionParams["tool"]
			slog.Warn("ParseTerminalPlan: quote_reply missing reply_to_message_id, degrading to plain speak",
				"decision_id", decisionID,
			)
			return replydomain.ReplyPlan{
				PlanID:         decisionID + "-plan",
				Intent:         session.Intent,
				Bubbles:        cleanedBubbles,
				PlannedActions: []policydomain.DecisionAction{policydomain.ActionReply},
				SendMode:       "group",
				FallbackText:   cleanedText,
			}, true, nil
		}
		return replydomain.ReplyPlan{
			PlanID:           decisionID + "-plan",
			Intent:           session.Intent,
			ReplyToMessageID: result.ReplyToMessageID,
			Bubbles:          cleanedBubbles,
			PlannedActions:   []policydomain.DecisionAction{policydomain.ActionReply},
			ActionParams:     map[string]any{"tool": "quote_reply"},
			SendMode:         "group",
			FallbackText:     cleanedText,
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
	preview := args.Text
	if len(preview) > 80 {
		preview = preview[:80] + "..."
	}
	slog.Debug("tool: speak_text", "bubbles", len(args.Bubbles), "reply_to", args.ReplyToMessageID, "text", preview)
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
		Desc: "Choose silence as the final action when replying would be socially unnatural or risky. Do NOT use this merely because the topic is unfamiliar — search first, then decide.",
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
	slog.Debug("tool: stay_silent", "reason", args.ReasonCode, "ttl_ms", args.TTLMS)
	return marshal(map[string]any{
		"tool":        "stay_silent",
		"reason_code": strings.TrimSpace(args.ReasonCode),
		"ttl_ms":      args.TTLMS,
	})
}

type reactEmojiTool struct{}

type reactEmojiArgs struct {
	EmojiID    string `json:"emoji_id"`
	MessageID  string `json:"message_id"`
	ReasonCode string `json:"reason_code"`
}

type reactEmojiResult struct {
	Tool      string `json:"tool"`
	EmojiID   string `json:"emoji_id"`
	MessageID string `json:"message_id"`
}

func newReactEmojiTool() *reactEmojiTool { return &reactEmojiTool{} }
func (t *reactEmojiTool) Name() string   { return "react_emoji" }
func (t *reactEmojiTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.Name(),
		Desc: "对某条消息点一个表情回应就结束本轮（不说话）。适合「看了但不必回复」的场景：接梗点赞、认可对方说法、图片好看。常用 emoji_id：76（赞）4468（笑哭）78089（敬礼）28487（doge）。msg_id 不填则默认回应触发本轮的那条消息。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"emoji_id":    {Type: schema.String, Required: true, Desc: "QQ 表情回应 ID，如 76=赞。"},
			"message_id":  {Type: schema.String, Desc: "要回应的消息 msg_id，缺省回应当前触发消息。"},
			"reason_code": {Type: schema.String, Desc: "Short reason code."},
		}),
	}, nil
}

func (t *reactEmojiTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args reactEmojiArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("decode react_emoji args: %w", err)
	}
	if strings.TrimSpace(args.EmojiID) == "" {
		args.EmojiID = "76"
	}
	return marshal(reactEmojiResult{Tool: t.Name(), EmojiID: args.EmojiID, MessageID: args.MessageID})
}

type queryMemoryTool struct {
	store     ports.MemoryStore
	retriever *retrievalsvc.Service
	session   replydomain.ToolContext
}

type queryMemoryArgs struct {
	Query       string   `json:"query"`
	Scope       string   `json:"scope"`
	TopK        int      `json:"top_k"`
	MemoryTypes []string `json:"memory_types"`
}

func newQueryMemoryTool(store ports.MemoryStore, retriever *retrievalsvc.Service, session replydomain.ToolContext) *queryMemoryTool {
	return &queryMemoryTool{store: store, retriever: retriever, session: session}
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
	slog.Debug("tool: query_memory", "query", args.Query, "scope", args.Scope, "top_k", args.TopK)
	query := ports.MemoryQuery{
		GroupID: t.session.GroupID,
		UserID:  t.session.UserID,
		Query:   args.Query,
		TopK:    clamp(args.TopK, 1, 5),
		Scope:   args.Scope,
		Types:   args.MemoryTypes,
	}
	var records []memorydomain.MemoryRecord
	var err error
	if t.retriever != nil {
		records, err = t.retriever.SearchMemories(ctx, query)
	} else {
		records, err = t.store.QueryMemories(ctx, query)
	}
	if err != nil {
		return "", err
	}
	slog.Debug("tool: query_memory result", "count", len(records))
	return marshal(map[string]any{"records": records})
}

type searchMemeTool struct {
	store   ports.MemeStore
	memeSvc *memesvc.Service
	session replydomain.ToolContext
}

type searchMemeArgs struct {
	Query         string `json:"query"`
	Emotion       string `json:"emotion"`
	Scene         string `json:"scene"`
	TopK          int    `json:"top_k"`
	ExcludeRecent bool   `json:"exclude_recent"`
}

func newSearchMemeTool(store ports.MemeStore, svc *memesvc.Service, session replydomain.ToolContext) *searchMemeTool {
	return &searchMemeTool{store: store, memeSvc: svc, session: session}
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
	slog.Debug("tool: search_meme", "query", args.Query, "emotion", args.Emotion, "scene", args.Scene)
	query := ports.MemeQuery{
		GroupID:       t.session.GroupID,
		Query:         args.Query,
		Emotion:       args.Emotion,
		Scene:         args.Scene,
		ExcludeRecent: args.ExcludeRecent,
	}
	if args.TopK > 0 {
		query.TopK = clamp(args.TopK, 1, 5)
	}
	var (
		results []mediadomain.MemeSearchResult
		err     error
	)
	if t.memeSvc != nil {
		results, err = t.memeSvc.Search(ctx, query)
	} else {
		results, err = t.store.SearchMemes(ctx, query)
	}
	if err != nil {
		return "", err
	}
	return marshal(map[string]any{"results": results})
}

type sendMemeTool struct {
	store ports.MemeStore
}

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

func newSendMemeTool(store ports.MemeStore) *sendMemeTool { return &sendMemeTool{store: store} }
func (t *sendMemeTool) Name() string                      { return "send_meme" }
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
func (t *sendMemeTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args sendMemeArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", err
	}
	if t.store != nil {
		asset, _, err := t.store.GetMeme(ctx, args.MemeID)
		if err != nil || asset.Status != "approved" {
			return marshal(map[string]any{
				"error": "meme_not_found",
				"hint":  "use search_meme to find a valid meme_id first",
			})
		}
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
			"reply_to_message_id": {Type: schema.String, Desc: "Message ID to quote; if omitted the reply is sent without quoting."},
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
		Desc: "撤回自己最近发出的一条消息。用于社交修复：发现刚说的话内容错了、发错对象、玩笑过了会冒犯人时，撤回比留着更得体。不要撤回正常内容。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"message_id":  {Type: schema.String, Required: true, Desc: "要撤回的 bot 消息 ID。"},
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

type markMemoryIntentTool struct {
	memSvc  *memsvc.Service
	session replydomain.ToolContext
}

type markMemoryIntentArgs struct {
	MemoryType      string  `json:"memory_type"`
	Subject         string  `json:"subject"`
	Content         string  `json:"content"`
	Importance      float64 `json:"importance"`
	EvidenceEventID string  `json:"evidence_event_id"`
}

func newMarkMemoryIntentTool(memSvc *memsvc.Service, session replydomain.ToolContext) *markMemoryIntentTool {
	return &markMemoryIntentTool{memSvc: memSvc, session: session}
}

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
	if t.memSvc != nil {
		scope := fmt.Sprintf("group:%d", t.session.GroupID)
		writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		record, err := t.memSvc.MarkIntent(writeCtx, memsvc.WriteIntent{
			Scope:         scope,
			MemoryType:    args.MemoryType,
			Subject:       args.Subject,
			Content:       args.Content,
			SourceEventID: args.EvidenceEventID,
			Importance:    args.Importance,
			Confidence:    0.7,
		})
		if err != nil {
			slog.Warn("mark_memory_intent: write failed", "scope", scope, "memory_type", args.MemoryType, "err", err)
		} else {
			intentID = record.MemoryID
		}
	}
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
	return min(max(value, minValue), maxValue)
}

func appendUnique(items []string, value string, max int) []string {
	value = strings.TrimSpace(value)
	if value == "" || slices.Contains(items, value) {
		return items
	}
	items = append(items, value)
	if max > 0 && len(items) > max {
		items = items[len(items)-max:]
	}
	return items
}

func clampF(v, lo, hi float64) float64 {
	return min(max(v, lo), hi)
}

// update_affinity tool

type updateAffinityTool struct {
	store     ports.ProfileStore
	session   replydomain.ToolContext
	personaID string
}

type updateAffinityArgs struct {
	UserID int64   `json:"user_id"`
	Delta  float64 `json:"delta"`
	Reason string  `json:"reason"`
}

func newUpdateAffinityTool(store ports.ProfileStore, session replydomain.ToolContext, personaID string) *updateAffinityTool {
	return &updateAffinityTool{store: store, session: session, personaID: personaID}
}

func (t *updateAffinityTool) Name() string { return "update_affinity" }

func (t *updateAffinityTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.Name(),
		Desc: "Adjust your affinity toward a group member by a signed delta. Use positive for warmth, negative for discomfort. Call at most once per reply.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"user_id": {Type: schema.Integer, Required: true, Desc: "Target member user ID."},
			"delta":   {Type: schema.Number, Required: true, Desc: "Affinity delta in [-0.3, 0.3]. Positive = warmer, negative = cooler."},
			"reason":  {Type: schema.String, Desc: "Short reason code for the change."},
		}),
	}, nil
}

func (t *updateAffinityTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	if t.store == nil {
		return marshal(map[string]any{"accepted": false, "reason": "no_store"})
	}
	// Budget check: at most 1 call per plan
	if t.session.Budget != nil {
		if t.session.Budget["update_affinity"] >= 1 {
			return marshal(map[string]any{"accepted": false, "reason": "budget_exceeded"})
		}
		t.session.Budget["update_affinity"]++
	}
	var args updateAffinityArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("decode update_affinity args: %w", err)
	}
	delta := clampF(args.Delta, -0.3, 0.3)
	rel, err := t.store.GetRelationship(ctx, t.personaID, t.session.GroupID, args.UserID)
	if err != nil {
		return "", err
	}
	rel.PersonaID = t.personaID
	rel.GroupID = t.session.GroupID
	rel.UserID = args.UserID
	rel.Affinity = clampF(rel.Affinity+delta, 0, 1)
	rel.LastInteractAt = time.Now()
	if err := t.store.SaveRelationship(ctx, rel); err != nil {
		return "", err
	}
	slog.Debug("tool: update_affinity", "user_id", args.UserID, "delta", delta, "new_affinity", rel.Affinity)
	return marshal(map[string]any{
		"accepted":     true,
		"new_affinity": rel.Affinity,
	})
}

// update_member_profile tool

type updateMemberProfileTool struct {
	store     ports.ProfileStore
	session   replydomain.ToolContext
	personaID string
}

type traitPatch struct {
	TraitType  string  `json:"trait_type"`
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
}

type updateMemberProfileArgs struct {
	UserID           int64        `json:"user_id"`
	AddTraits        []traitPatch `json:"add_traits"`
	AddTags          []string     `json:"add_tags"`
	AddInterests     []string     `json:"add_interests"`
	FamiliarityDelta float64      `json:"familiarity_delta"`
	EvidenceEventID  string       `json:"evidence_event_id"`
}

func newUpdateMemberProfileTool(store ports.ProfileStore, session replydomain.ToolContext, personaID string) *updateMemberProfileTool {
	return &updateMemberProfileTool{store: store, session: session, personaID: personaID}
}

func (t *updateMemberProfileTool) Name() string { return "update_member_profile" }

func (t *updateMemberProfileTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.Name(),
		Desc: "Update a member's traits, tags, or interests when you learn new information about them. Also optionally adjust familiarity. Call at most once per reply.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"user_id":           {Type: schema.Integer, Required: true, Desc: "Target member user ID."},
			"add_traits":        {Type: schema.Array, Desc: "List of {trait_type, value, confidence} to add or update."},
			"add_tags":          {Type: schema.Array, Desc: "Tags to add (e.g. 'funny', 'gamer')."},
			"add_interests":     {Type: schema.Array, Desc: "Interests to add (e.g. 'anime', 'coding')."},
			"familiarity_delta": {Type: schema.Number, Desc: "Optional familiarity delta in [-0.2, 0.2]. Suggest <=0.1 per call."},
			"evidence_event_id": {Type: schema.String, Desc: "Event ID that supports this update."},
		}),
	}, nil
}

func (t *updateMemberProfileTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	if t.store == nil {
		return marshal(map[string]any{"accepted": false, "reason": "no_store"})
	}
	// Budget check: at most 1 call per plan
	if t.session.Budget != nil {
		if t.session.Budget["update_member_profile"] >= 1 {
			return marshal(map[string]any{"accepted": false, "reason": "budget_exceeded"})
		}
		t.session.Budget["update_member_profile"]++
	}
	var args updateMemberProfileArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("decode update_member_profile args: %w", err)
	}

	profile, err := t.store.GetMemberProfile(ctx, t.session.GroupID, args.UserID)
	if err != nil {
		return "", err
	}

	now := time.Now()
	// Merge traits: deduplicate by (TraitType+Value), update confidence if exists
	for _, p := range args.AddTraits {
		merged := false
		for i, existing := range profile.Traits {
			if existing.TraitType == p.TraitType && existing.Value == p.Value {
				profile.Traits[i].Confidence = clampF(p.Confidence, 0, 1)
				profile.Traits[i].UpdatedAt = now
				profile.Traits[i].EvidenceEventID = args.EvidenceEventID
				merged = true
				break
			}
		}
		if !merged {
			profile.Traits = append(profile.Traits, profiledomain.MemberTrait{
				GroupID:         t.session.GroupID,
				UserID:          args.UserID,
				TraitType:       p.TraitType,
				Value:           p.Value,
				Confidence:      clampF(p.Confidence, 0, 1),
				EvidenceEventID: args.EvidenceEventID,
				UpdatedAt:       now,
			})
		}
	}
	// Cap at 30 traits (sliding window)
	if len(profile.Traits) > 30 {
		profile.Traits = profile.Traits[len(profile.Traits)-30:]
	}
	for _, tag := range args.AddTags {
		profile.Tags = appendUnique(profile.Tags, tag, 20)
	}
	for _, interest := range args.AddInterests {
		profile.Interests = appendUnique(profile.Interests, interest, 20)
	}

	if err := t.store.SaveMemberProfile(ctx, profile); err != nil {
		return "", err
	}

	// Update familiarity if delta provided (ignored in observe-only mode)
	if args.FamiliarityDelta != 0 && !t.session.ObserveOnly {
		rel, err := t.store.GetRelationship(ctx, t.personaID, t.session.GroupID, args.UserID)
		if err != nil {
			return "", err
		}
		rel.PersonaID = t.personaID
		rel.GroupID = t.session.GroupID
		rel.UserID = args.UserID
		rel.Familiarity = clampF(rel.Familiarity+clampF(args.FamiliarityDelta, -0.2, 0.2), 0, 0.8)
		rel.LastInteractAt = now
		if err := t.store.SaveRelationship(ctx, rel); err != nil {
			return "", err
		}
	}

	slog.Debug("tool: update_member_profile", "user_id", args.UserID,
		"traits_added", len(args.AddTraits), "tags_added", len(args.AddTags))
	return marshal(map[string]any{"accepted": true})
}
