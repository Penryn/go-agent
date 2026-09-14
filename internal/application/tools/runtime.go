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
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	memesvc "github.com/phlin/go-agent/internal/application/meme"
	memsvc "github.com/phlin/go-agent/internal/application/memory"
	modelusagesvc "github.com/phlin/go-agent/internal/application/modelusage"
	"github.com/phlin/go-agent/internal/application/ports"
	relationshipsvc "github.com/phlin/go-agent/internal/application/relationship"
	retrievalsvc "github.com/phlin/go-agent/internal/application/retrieval"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

type Runtime struct {
	memeStore         ports.MemeStore
	profileStore      ports.ProfileStore
	personaFacts      ports.PersonaFactStore
	memory            *memsvc.Service
	relationships     *relationshipsvc.Service
	personaID         string
	personaDefinition personadomain.PersonaDefinition
	personaFactAdmins []int64
	memeSvc           *memesvc.Service
	retriever         *retrievalsvc.Service
	external          []registeredTool
	externalMu        sync.RWMutex
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

func WithMemoryService(service *memsvc.Service) Option {
	return func(rt *Runtime) { rt.memory = service }
}

func WithRelationshipService(service *relationshipsvc.Service) Option {
	return func(rt *Runtime) { rt.relationships = service }
}

func WithPersonaFactAdmins(userIDs []int64) Option {
	return func(rt *Runtime) { rt.personaFactAdmins = append([]int64(nil), userIDs...) }
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
		if !internalToolAllowed(session.AllowedTools, candidate.Name()) {
			continue
		}
		all = append(all, registeredTool{
			name:     candidate.Name(),
			tool:     candidate,
			terminal: isTerminalTool(candidate.Name()),
		})
	}
	r.externalMu.RLock()
	external := append([]registeredTool(nil), r.external...)
	r.externalMu.RUnlock()
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

// 类型定义已移至 types.go
// 辅助函数已移至 helpers.go

func (r *Runtime) Tools(session replydomain.ToolContext) []tool.BaseTool {
	available := r.availableTools(session)
	result := make([]tool.BaseTool, 0, len(available))
	for _, candidate := range available {
		result = append(result, modelusagesvc.WrapTool(candidate.tool))
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

// RegisterTools adds tools discovered at startup (for example MCP and Codex)
// while preserving the existing per-group allowlist behavior.
func (r *Runtime) RegisterTools(ctx context.Context, tools ...tool.BaseTool) error {
	r.externalMu.Lock()
	defer r.externalMu.Unlock()
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

// ReplaceMCPTools atomically swaps tools discovered from MCP servers while
// retaining other external tools such as the Codex delegate.
func (r *Runtime) ReplaceMCPTools(ctx context.Context, tools ...tool.BaseTool) error {
	r.externalMu.Lock()
	defer r.externalMu.Unlock()

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
	retained := make([]registeredTool, 0, len(r.external))
	for _, candidate := range r.external {
		if strings.HasPrefix(candidate.name, "mcp_") {
			continue
		}
		known[candidate.name] = true
		retained = append(retained, candidate)
	}
	for _, candidate := range tools {
		info, err := candidate.Info(ctx)
		if err != nil {
			return fmt.Errorf("read MCP tool info: %w", err)
		}
		if info == nil || strings.TrimSpace(info.Name) == "" {
			return errors.New("MCP tool name is required")
		}
		if known[info.Name] {
			return fmt.Errorf("duplicate tool name %q", info.Name)
		}
		known[info.Name] = true
		retained = append(retained, registeredTool{name: info.Name, tool: candidate, external: true})
	}
	r.external = retained
	return nil
}

func (r *Runtime) replyTools(session replydomain.ToolContext) []namedTool {
	result := []namedTool{
		newSpeakTextTool(),
		newStaySilentTool(),
		newReactEmojiTool(),
		newSendMemeTool(r.memeStore),
		newQuoteReplyTool(),
	}
	if len(session.RecallableMessageIDs) > 0 {
		result = append(result, newRepairMessageTool(session))
	}
	if session.TriggerType == "poke_reply" {
		result = append(result, newPokeMemberTool())
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
		newRememberMemoryTool(r.memory, session),
		newRelationshipSignalTool(r.relationships, session),
		newUpdatePersonaFactTool(r.personaFacts, session, r.personaDefinition, r.personaFactAdmins),
	}
}

// namedTool 定义已移至 types.go

type searchMemeTool struct {
	memeSvc *memesvc.Service
	session replydomain.ToolContext
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

type repairMessageTool struct {
	session replydomain.ToolContext
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

func clampF(v, lo, hi float64) float64 {
	return min(max(v, lo), hi)
}

// update_persona_fact tool

type updatePersonaFactTool struct {
	store      ports.PersonaFactStore
	session    replydomain.ToolContext
	definition personadomain.PersonaDefinition
	admins     []int64
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
