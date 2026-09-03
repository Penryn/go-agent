package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	memesvc "github.com/phlin/go-agent/internal/application/meme"
	memsvc "github.com/phlin/go-agent/internal/application/memory"
	"github.com/phlin/go-agent/internal/application/ports"
	retrievalsvc "github.com/phlin/go-agent/internal/application/retrieval"
	"github.com/phlin/go-agent/internal/application/textutil"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

type Runtime struct {
	memeStore         ports.MemeStore
	profileStore      ports.ProfileStore
	personaFacts      ports.PersonaFactStore
	personaID         string
	personaDefinition personadomain.PersonaDefinition
	personaFactAdmins []int64
	memSvc            *memsvc.Service
	memeSvc           *memesvc.Service
	retriever         *retrievalsvc.Service
	external          []registeredTool
	approvals         *WriteApprovalStore
}

type Option func(*Runtime)

func NewRuntime(memeStore ports.MemeStore, opts ...Option) *Runtime {
	rt := &Runtime{
		memeStore: memeStore,
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

func WithPersonaDefinition(definition personadomain.PersonaDefinition) Option {
	return func(rt *Runtime) {
		rt.personaDefinition = definition
		rt.personaID = definition.Config.ID
	}
}

func WithPersonaFactStore(store ports.PersonaFactStore) Option {
	return func(rt *Runtime) { rt.personaFacts = store }
}

func WithPersonaFactAdmins(userIDs []int64) Option {
	return func(rt *Runtime) { rt.personaFactAdmins = append([]int64(nil), userIDs...) }
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

func (r *Runtime) ToolContext(ctx context.Context, groupID, userID int64) context.Context {
	return withToolIdentity(ctx, groupID, userID)
}

func (r *Runtime) availableTools(session replydomain.ToolContext) []registeredTool {
	internal := make([]namedTool, 0, 13)
	internal = append(internal, r.replyTools(session)...)
	internal = append(internal, r.knowledgeTools(session)...)
	internal = append(internal, r.profileTools(session)...)
	all := make([]registeredTool, 0, len(internal)+len(r.external))
	for _, candidate := range internal {
		allowed := internalToolAllowed(session.AllowedTools, candidate.Name())
		all = append(all, registeredTool{
			name:     candidate.Name(),
			tool:     gateTool(candidate, allowed),
			terminal: isTerminalTool(candidate.Name()),
		})
	}
	external := append([]registeredTool(nil), r.external...)
	sort.SliceStable(external, func(i, j int) bool { return external[i].name < external[j].name })
	for _, candidate := range external {
		// External tools remain opt-in. Their definitions are loaded at startup,
		// while group policy decides whether they are exposed to the model.
		if slices.Contains(session.AllowedTools, candidate.name) {
			all = append(all, candidate)
		}
	}
	return all
}

type gatedTool struct {
	tool tool.InvokableTool
}

func gateTool(candidate tool.BaseTool, allowed bool) tool.BaseTool {
	if allowed {
		return candidate
	}
	invokable, ok := candidate.(tool.InvokableTool)
	if !ok {
		return candidate
	}
	return &gatedTool{tool: invokable}
}

func (t *gatedTool) Info(ctx context.Context) (*schema.ToolInfo, error) { return t.tool.Info(ctx) }

func (t *gatedTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	return "", fmt.Errorf("tool %q is not allowed in this group", t.toolName())
}

func (t *gatedTool) toolName() string {
	info, err := t.tool.Info(context.Background())
	if err == nil && info != nil {
		return info.Name
	}
	return "unknown"
}

func (r *Runtime) Tools(session replydomain.ToolContext) []tool.BaseTool {
	available := r.availableTools(session)
	result := make([]tool.BaseTool, 0, len(available))
	for _, candidate := range available {
		result = append(result, candidate.tool)
	}
	return result
}

// TerminalTools returns the tools that end the agent loop and produce the
// single outward ReplyPlan for this turn.
func (r *Runtime) TerminalTools(session replydomain.ToolContext) map[string]bool {
	result := make(map[string]bool)
	for _, candidate := range r.availableTools(session) {
		if candidate.terminal && (!candidate.external || slices.Contains(session.AllowedTools, candidate.name)) &&
			internalToolAllowed(session.AllowedTools, candidate.name) {
			result[candidate.name] = true
		}
	}
	return result
}

func internalToolAllowed(allowlist []string, name string) bool {
	return len(allowlist) == 0 || slices.Contains(allowlist, name)
}

type registeredTool struct {
	name     string
	tool     tool.BaseTool
	external bool
	// terminal 工具终结 agent 循环,产出本轮对外的 ReplyPlan。
	terminal bool
}

func isTerminalTool(name string) bool {
	switch name {
	case "speak_text", "stay_silent", "react_emoji", "send_meme", "quote_reply", "repair_message", "poke_member":
		return true
	}
	return false
}

// RegisterTools adds tools discovered at startup (for example MCP and Codex)
// while preserving the existing per-group allowlist behavior.
func (r *Runtime) RegisterTools(ctx context.Context, tools ...tool.BaseTool) error {
	known := make(map[string]bool, len(r.external)+13)
	for _, candidate := range r.allReplyTools() {
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

func (r *Runtime) replyTools(session replydomain.ToolContext) []namedTool {
	result := []namedTool{
		newSpeakTextTool(),
		newStaySilentTool(),
		newReactEmojiTool(),
		newSendMemeTool(r.memeStore),
		newQuoteReplyTool(),
		newRepairMessageTool(session),
		newPokeMemberTool(),
	}
	return result
}

func (r *Runtime) allReplyTools() []namedTool {
	return r.replyTools(replydomain.ToolContext{
		TriggerType:          "poke_reply",
		RecallableMessageIDs: []string{"placeholder"},
	})
}

func (r *Runtime) knowledgeTools(session replydomain.ToolContext) []namedTool {
	return []namedTool{
		newQueryMemoryTool(r.retriever, session),
		newSearchMemeTool(r.memeSvc, session),
	}
}

func (r *Runtime) profileTools(session replydomain.ToolContext) []namedTool {
	return []namedTool{
		newQueryMemberProfileTool(r.profileStore, session),
		newMarkMemoryIntentTool(r.memSvc, session),
		newUpdateAffinityTool(r.profileStore, session, r.personaID),
		newUpdateMemberProfileTool(r.profileStore, session, r.personaID),
		newUpdatePersonaFactTool(r.personaFacts, session, r.personaDefinition, r.personaFactAdmins),
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
			bubbles = textutil.SplitNaturalBubbles(cleanedText, 2)
		}
		return replydomain.ReplyPlan{
			PlanID:               decisionID + "-plan",
			Intent:               session.Intent,
			ReplyToMessageID:     result.ReplyToMessageID,
			Bubbles:              bubbles,
			PlannedActions:       []policydomain.DecisionAction{policydomain.ActionReply},
			SendMode:             "group",
			FallbackText:         cleanedText,
			ProposedPersonaFacts: append([]replydomain.PersonaFactCandidate(nil), result.SelfFacts...),
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
		if len(cleanedBubbles) == 0 && cleanedText != "" {
			cleanedBubbles = textutil.SplitNaturalBubbles(cleanedText, 2)
		}
		if result.ReplyToMessageID == "" {
			// LLM 未提供引用 ID，降级为普通发言，不设置 ActionParams["tool"]
			slog.Warn("ParseTerminalPlan: quote_reply missing reply_to_message_id, degrading to plain speak",
				"decision_id", decisionID,
			)
			return replydomain.ReplyPlan{
				PlanID:               decisionID + "-plan",
				Intent:               session.Intent,
				Bubbles:              cleanedBubbles,
				PlannedActions:       []policydomain.DecisionAction{policydomain.ActionReply},
				SendMode:             "group",
				FallbackText:         cleanedText,
				ProposedPersonaFacts: append([]replydomain.PersonaFactCandidate(nil), result.SelfFacts...),
			}, true, nil
		}
		return replydomain.ReplyPlan{
			PlanID:               decisionID + "-plan",
			Intent:               session.Intent,
			ReplyToMessageID:     result.ReplyToMessageID,
			Bubbles:              cleanedBubbles,
			PlannedActions:       []policydomain.DecisionAction{policydomain.ActionReply},
			ActionParams:         map[string]any{"tool": "quote_reply"},
			SendMode:             "group",
			FallbackText:         cleanedText,
			ProposedPersonaFacts: append([]replydomain.PersonaFactCandidate(nil), result.SelfFacts...),
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
	case "repair_message":
		var result repairMessageResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return replydomain.ReplyPlan{}, false, fmt.Errorf("decode repair_message result: %w", err)
		}
		correctedText := textutil.StripThinkBlocks(result.CorrectedText)
		return replydomain.ReplyPlan{
			PlanID:           decisionID + "-plan",
			Intent:           session.Intent,
			ReplyToMessageID: result.ReplyToMessageID,
			Bubbles:          compactStrings([]string{correctedText}, 1),
			PlannedActions:   []policydomain.DecisionAction{policydomain.ActionRepair},
			ActionParams: map[string]any{
				"message_id":          result.MessageID,
				"corrected_text":      correctedText,
				"reply_to_message_id": result.ReplyToMessageID,
			},
			SendMode:     "group",
			FallbackText: correctedText,
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
	Text             string                             `json:"text"`
	Bubbles          []string                           `json:"bubbles"`
	ReplyToMessageID string                             `json:"reply_to_message_id"`
	SelfFacts        []replydomain.PersonaFactCandidate `json:"self_facts"`
}

type speakTextResult struct {
	Tool             string                             `json:"tool"`
	Text             string                             `json:"text"`
	Bubbles          []string                           `json:"bubbles"`
	ReplyToMessageID string                             `json:"reply_to_message_id"`
	SelfFacts        []replydomain.PersonaFactCandidate `json:"self_facts"`
}

func newSpeakTextTool() *speakTextTool { return &speakTextTool{} }

func (t *speakTextTool) Name() string { return "speak_text" }

func (t *speakTextTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.Name(),
		Desc: "Send a natural conversational reply in the current group. Use bubbles only when a genuine pause or change of thought makes separate messages feel natural.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"text":                {Type: schema.String, Required: true, Desc: "Primary reply text."},
			"bubbles":             {Type: schema.Array, Desc: "Optional separate message bubbles when the reply naturally pauses or changes thought."},
			"reply_to_message_id": {Type: schema.String, Desc: "Optional message ID to quote-reply."},
			"self_facts":          selfFactsParameter(),
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
		SelfFacts:        append([]replydomain.PersonaFactCandidate(nil), args.SelfFacts...),
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
	retriever *retrievalsvc.Service
	session   replydomain.ToolContext
}

type queryMemoryArgs struct {
	Query       string   `json:"query"`
	Scope       string   `json:"scope"`
	TopK        int      `json:"top_k"`
	MemoryTypes []string `json:"memory_types"`
}

func newQueryMemoryTool(retriever *retrievalsvc.Service, session replydomain.ToolContext) *queryMemoryTool {
	return &queryMemoryTool{retriever: retriever, session: session}
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
	if t.retriever == nil {
		return "", errors.New("query_memory: retriever is not configured")
	}
	records, err := t.retriever.SearchMemories(ctx, query)
	if err != nil {
		return "", err
	}
	slog.Debug("tool: query_memory result", "count", len(records))
	return marshal(map[string]any{"records": records})
}

type searchMemeTool struct {
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

func newSearchMemeTool(svc *memesvc.Service, session replydomain.ToolContext) *searchMemeTool {
	return &searchMemeTool{memeSvc: svc, session: session}
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
	if t.memeSvc == nil {
		return "", errors.New("search_meme: service is not configured")
	}
	results, err := t.memeSvc.Search(ctx, query)
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
	ReplyToMessageID string                             `json:"reply_to_message_id"`
	Text             string                             `json:"text"`
	Bubbles          []string                           `json:"bubbles"`
	SelfFacts        []replydomain.PersonaFactCandidate `json:"self_facts"`
}
type quoteReplyResult struct {
	Tool             string                             `json:"tool"`
	ReplyToMessageID string                             `json:"reply_to_message_id"`
	Text             string                             `json:"text"`
	Bubbles          []string                           `json:"bubbles"`
	SelfFacts        []replydomain.PersonaFactCandidate `json:"self_facts"`
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
			"self_facts":          selfFactsParameter(),
		}),
	}, nil
}
func (t *quoteReplyTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args quoteReplyArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", err
	}
	return marshal(quoteReplyResult{Tool: t.Name(), ReplyToMessageID: args.ReplyToMessageID, Text: strings.TrimSpace(args.Text), Bubbles: compactStrings(args.Bubbles, 2), SelfFacts: append([]replydomain.PersonaFactCandidate(nil), args.SelfFacts...)})
}

func selfFactsParameter() *schema.ParameterInfo {
	return &schema.ParameterInfo{
		Type: schema.Array,
		Desc: "New fictional first-person facts explicitly stated in this exact reply. Leave empty when no new self fact is introduced.",
		ElemInfo: &schema.ParameterInfo{
			Type: schema.Object,
			SubParams: map[string]*schema.ParameterInfo{
				"key":           {Type: schema.String, Required: true, Desc: "Stable namespaced key such as education.high_school_major."},
				"value":         {Type: schema.String, Required: true, Desc: "Concise canonical value."},
				"evidence_text": {Type: schema.String, Required: true, Desc: "Exact first-person phrase present in the final reply."},
				"correction":    {Type: schema.Boolean, Desc: "True only when the reply explicitly corrects an earlier canon fact."},
			},
		},
	}
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

type repairMessageTool struct {
	session replydomain.ToolContext
}
type repairMessageArgs struct {
	MessageID        string `json:"message_id"`
	CorrectedText    string `json:"corrected_text"`
	ReplyToMessageID string `json:"reply_to_message_id"`
	ReasonCode       string `json:"reason_code"`
}
type repairMessageResult struct {
	Tool             string `json:"tool"`
	MessageID        string `json:"message_id"`
	CorrectedText    string `json:"corrected_text"`
	ReplyToMessageID string `json:"reply_to_message_id"`
}

func newRepairMessageTool(session replydomain.ToolContext) *repairMessageTool {
	return &repairMessageTool{session: session}
}
func (t *repairMessageTool) Name() string { return "repair_message" }
func (t *repairMessageTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.Name(),
		Desc: "Repair one of your recent messages by recalling it and optionally sending a corrected replacement. Only message IDs supplied in the current context are accepted.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"message_id":          {Type: schema.String, Required: true, Desc: "Recent bot message ID to recall."},
			"corrected_text":      {Type: schema.String, Desc: "Optional corrected replacement text."},
			"reply_to_message_id": {Type: schema.String, Desc: "Optional user message ID to quote in the replacement."},
			"reason_code":         {Type: schema.String, Desc: "Short reason for the repair."},
		}),
	}, nil
}
func (t *repairMessageTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args repairMessageArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", err
	}
	if !slices.Contains(t.session.RecallableMessageIDs, args.MessageID) {
		return "", errors.New("repair_message: message_id is not a recent bot message")
	}
	return marshal(repairMessageResult{
		Tool:             t.Name(),
		MessageID:        args.MessageID,
		CorrectedText:    strings.TrimSpace(args.CorrectedText),
		ReplyToMessageID: args.ReplyToMessageID,
	})
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

	// Update familiarity if delta provided
	if args.FamiliarityDelta != 0 {
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

// update_persona_fact tool

type updatePersonaFactTool struct {
	store      ports.PersonaFactStore
	session    replydomain.ToolContext
	definition personadomain.PersonaDefinition
	admins     []int64
}

type updatePersonaFactArgs struct {
	Key             string  `json:"key"`
	Value           string  `json:"value"`
	SourceKind      string  `json:"source_kind"`
	EvidenceEventID string  `json:"evidence_event_id"`
	Confidence      float64 `json:"confidence"`
	TTLHours        int     `json:"ttl_hours"`
}

func newUpdatePersonaFactTool(store ports.PersonaFactStore, session replydomain.ToolContext, definition personadomain.PersonaDefinition, admins []int64) *updatePersonaFactTool {
	return &updatePersonaFactTool{store: store, session: session, definition: definition, admins: append([]int64(nil), admins...)}
}

func (t *updatePersonaFactTool) Name() string { return "update_persona_fact" }

func (t *updatePersonaFactTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.Name(),
		Desc: "Record a sourced change in the unified persona fact model. The configured fact policy decides whether an operator update is permitted.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"key":               {Type: schema.String, Required: true, Desc: "A registered canonical persona fact key or configured legacy alias."},
			"value":             {Type: schema.String, Required: true, Desc: "Concise current fact, without roleplay or speculation."},
			"source_kind":       {Type: schema.String, Required: true, Desc: "One of: owner_statement, group_report, web_search."},
			"evidence_event_id": {Type: schema.String, Required: true, Desc: "Current event ID supporting the update."},
			"confidence":        {Type: schema.Number, Desc: "Confidence in [0,1]."},
			"ttl_hours":         {Type: schema.Integer, Desc: "Reported-fact lifetime in hours; defaults to 72 and is capped at 168."},
		}),
	}, nil
}

func (t *updatePersonaFactTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	if t.store == nil {
		return marshal(map[string]any{"accepted": false, "reason": "no_store"})
	}
	if t.session.Budget != nil {
		if t.session.Budget[t.Name()] >= 1 {
			return marshal(map[string]any{"accepted": false, "reason": "budget_exceeded"})
		}
		t.session.Budget[t.Name()]++
	}
	var args updatePersonaFactArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("decode update_persona_fact args: %w", err)
	}
	args.Key = t.definition.CanonicalKey(args.Key)
	args.Value = strings.TrimSpace(args.Value)
	args.SourceKind = strings.TrimSpace(args.SourceKind)
	args.EvidenceEventID = strings.TrimSpace(args.EvidenceEventID)
	rule, registered := t.definition.Rule(args.Key)
	if !registered {
		return marshal(map[string]any{"accepted": false, "reason": "key_not_registered"})
	}
	if rule.Policy == personadomain.FactPolicyLocked || rule.Policy == personadomain.FactPolicyForbidden {
		return marshal(map[string]any{"accepted": false, "reason": "policy_rejects_update", "policy": rule.Policy})
	}
	if args.Value == "" || len([]rune(args.Value)) > 240 {
		return marshal(map[string]any{"accepted": false, "reason": "invalid_value"})
	}
	if args.EvidenceEventID == "" || args.EvidenceEventID != t.session.TriggerEventID {
		return marshal(map[string]any{"accepted": false, "reason": "evidence_must_be_current_event"})
	}

	now := time.Now()
	effectiveAt := now
	if t.session.TriggerTimestampUnix > 0 {
		effectiveAt = time.Unix(t.session.TriggerTimestampUnix, 0)
	}
	fact := personadomain.PersonaFact{
		PersonaID:       t.definition.Config.ID,
		Key:             args.Key,
		Value:           args.Value,
		SourceKind:      args.SourceKind,
		SourceGroupID:   t.session.GroupID,
		SourceUserID:    t.session.UserID,
		SourceEventID:   args.EvidenceEventID,
		EffectiveAt:     effectiveAt,
		RecordedAt:      now,
		DefinitionHash:  t.definition.Hash,
		ResolutionState: personadomain.FactResolutionActive,
	}
	switch args.SourceKind {
	case "owner_statement":
		if !slices.Contains(t.admins, t.session.UserID) {
			return marshal(map[string]any{"accepted": false, "reason": "owner_not_authorized"})
		}
		fact.Status = personadomain.PersonaFactVerified
		fact.Confidence = clampF(args.Confidence, 0.8, 1)
		if args.Confidence == 0 {
			fact.Confidence = 1
		}
	case "group_report", "web_search":
		fact.Status = personadomain.PersonaFactReported
		fact.Confidence = clampF(args.Confidence, 0.1, 0.8)
		if args.Confidence == 0 {
			fact.Confidence = 0.6
		}
		ttlHours := clamp(args.TTLHours, 1, 168)
		if args.TTLHours == 0 {
			ttlHours = 72
		}
		fact.ExpiresAt = now.Add(time.Duration(ttlHours) * time.Hour)
	default:
		return marshal(map[string]any{"accepted": false, "reason": "invalid_source_kind"})
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{
		fact.PersonaID, fact.Key, fact.Value, fact.Status, fact.SourceKind, fact.SourceEventID,
	}, "\x00")))
	fact.FactID = fmt.Sprintf("persona-fact-%x", digest[:12])
	if err := t.store.AppendPersonaFact(ctx, fact); err != nil {
		return "", err
	}
	return marshal(map[string]any{
		"accepted":   true,
		"fact_id":    fact.FactID,
		"status":     fact.Status,
		"expires_at": fact.ExpiresAt,
	})
}
