package group_actor

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/phlin/go-agent/internal/application/ports"
	"github.com/phlin/go-agent/internal/application/presence/ingress"
	socialdecisionsvc "github.com/phlin/go-agent/internal/application/socialdecision"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	feedbackdomain "github.com/phlin/go-agent/internal/domain/feedback"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
)

// 新增：社交决策相关接口
type DecisionEngine interface {
	DecideParticipation(ctx context.Context, req socialdecisionsvc.DecisionRequest) (*presencedomain.ParticipationDecision, error)
}

type PersonaAssembler interface {
	AssembleContext(ctx context.Context, config personadomain.PersonaConfig, groupID int64) (*personadomain.PersonaContext, error)
}

type ResponsePlanner interface {
	CreateResponsePlan(
		ctx context.Context,
		decision *presencedomain.ParticipationDecision,
		evt *conversationdomain.ConversationEvent,
		personaCtx *personadomain.PersonaContext,
	) (*presencedomain.ResponsePlan, error)
}

type ResponseExecutor interface {
	ExecutePlan(ctx context.Context, plan *presencedomain.ResponsePlan) (actionID string, err error)
}

type FeedbackCollector interface {
	StartFeedbackWindow(ctx context.Context, decisionID, actionID string, groupID int64, sentAt time.Time) (*presencedomain.FeedbackWindow, error)
	CollectFeedback(ctx context.Context, window *presencedomain.FeedbackWindow) (*feedbackdomain.ActionFeedback, error)
}

const defaultTailSize = 32
const defaultMaxSeen = 2048
const (
	burstWindow    = 700 * time.Millisecond
	burstMaxWindow = 3 * time.Second
)

type Manager struct {
	log           *ingress.MemoryEventLog
	archive       ports.MemoryStore
	state         WorkingMemoryStore
	tailSize      int
	maxSeen       int
	idleTTL       time.Duration

	// 新增：社交决策依赖
	decisionEngine    DecisionEngine
	personaAssembler  PersonaAssembler
	responsePlanner   ResponsePlanner
	responseExecutor  ResponseExecutor
	feedbackCollector FeedbackCollector
	personaID         string

	mu     sync.Mutex
	closed bool
	groups map[int64]*actor
}

type Option func(*Manager)

// WorkingMemoryStore persists the actor's rebuildable per-group projection.
type WorkingMemoryStore interface {
	LoadWorkingMemory(context.Context, int64) (presencedomain.GroupWorkingMemory, error)
	SaveWorkingMemory(context.Context, presencedomain.GroupWorkingMemory) error
}

// EventReplayStore is the optional durable event source used to rebuild a
// projection after its cache is lost or intentionally invalidated.
type EventReplayStore interface {
	EventsAfter(context.Context, int64, time.Time, string, int) ([]conversationdomain.ConversationEvent, error)
}

// WithArchive mirrors every observed event into the durable conversation
// store. The event log remains the fast perception path; archive failures are
// returned so callers can retry without losing the in-memory observation.
func WithArchive(store ports.MemoryStore) Option {
	return func(m *Manager) { m.archive = store }
}

func WithStateStore(store WorkingMemoryStore) Option {
	return func(m *Manager) { m.state = store }
}

// WithIdleTTL enables lifecycle reclamation for groups that have not received
// any actor operation for the given duration. A non-positive duration disables
// reclamation.
func WithIdleTTL(ttl time.Duration) Option {
	return func(m *Manager) {
		if ttl > 0 {
			m.idleTTL = ttl
		}
	}
}

// 新增：社交决策依赖配置
func WithDecisionEngine(engine DecisionEngine) Option {
	return func(m *Manager) { m.decisionEngine = engine }
}

func WithPersonaAssembler(assembler PersonaAssembler) Option {
	return func(m *Manager) { m.personaAssembler = assembler }
}

func WithResponsePlanner(planner ResponsePlanner) Option {
	return func(m *Manager) { m.responsePlanner = planner }
}

func WithResponseExecutor(executor ResponseExecutor) Option {
	return func(m *Manager) { m.responseExecutor = executor }
}

func WithFeedbackCollector(collector FeedbackCollector) Option {
	return func(m *Manager) { m.feedbackCollector = collector }
}

func WithPersonaID(personaID string) Option {
	return func(m *Manager) { m.personaID = personaID }
}

func NewManager(log *ingress.MemoryEventLog, opts ...Option) *Manager {
	m := &Manager{log: log, tailSize: defaultTailSize, maxSeen: defaultMaxSeen, groups: make(map[int64]*actor)}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Observe durably records the fact before handing it to the group actor. The
// actor only performs fast state updates, so perception does not wait on an LLM.
func (m *Manager) Observe(ctx context.Context, record presencedomain.EventRecord) (presencedomain.GroupWorkingMemory, error) {
	if m == nil || m.log == nil {
		return presencedomain.GroupWorkingMemory{}, errors.New("group actor: event log is nil")
	}
	if record.EventID == "" {
		return presencedomain.GroupWorkingMemory{}, errors.New("group actor: event id is required")
	}
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}
	if record.Origin == "" {
		record.Origin = presencedomain.OriginInbound
	}
	record.Event.Origin = string(record.Origin)
	// The durable archive is the source of truth for received facts. Write it
	// before advancing the in-memory deduplication cursor so a transient store
	// failure can be retried with the same event ID.
	if m.archive != nil {
		if err := m.archive.ArchiveEvent(ctx, record.Event); err != nil {
			return presencedomain.GroupWorkingMemory{}, err
		}
	}

	if _, err := m.log.AppendIfNew(ctx, record); err != nil {
		return presencedomain.GroupWorkingMemory{}, err
	}
	a, err := m.actor(ctx, record.GroupID)
	if err != nil {
		return presencedomain.GroupWorkingMemory{}, err
	}
	memory := a.observe(record)
	if err := m.save(ctx, memory); err != nil {
		return memory, err
	}
	return memory, nil
}

func (m *Manager) Snapshot(ctx context.Context, groupID int64) (presencedomain.GroupWorkingMemory, error) {
	a, err := m.actor(ctx, groupID)
	if err != nil {
		return presencedomain.GroupWorkingMemory{}, err
	}
	return a.snapshot(), nil
}

// Replay rebuilds the group projection from durable events after a cursor.
// Existing tail events are deduplicated by event ID, so replay is safe to
// retry and can be used after a partial cache write.
func (m *Manager) Replay(ctx context.Context, groupID int64, after time.Time, afterEventID string, limit int) (presencedomain.GroupWorkingMemory, error) {
	source, ok := m.state.(EventReplayStore)
	if !ok {
		return presencedomain.GroupWorkingMemory{}, errors.New("group actor: event replay store is not configured")
	}
	events, err := source.EventsAfter(ctx, groupID, after, afterEventID, limit)
	if err != nil {
		return presencedomain.GroupWorkingMemory{}, err
	}
	a, err := m.actor(ctx, groupID)
	if err != nil {
		return presencedomain.GroupWorkingMemory{}, err
	}
	a.mu.Lock()
	for _, event := range events {
		if event.EventID == "" {
			continue
		}
		if _, seen := a.seen[event.EventID]; seen {
			continue
		}
		timestamp := time.Unix(event.TimestampUnix, 0)
		if event.TimestampUnix == 0 {
			timestamp = time.Now()
		}
		origin := presencedomain.OriginInbound
		if event.Origin == string(presencedomain.OriginOutbound) {
			origin = presencedomain.OriginOutbound
		}
		record := presencedomain.EventRecord{EventID: event.EventID, GroupID: groupID, UserID: event.UserID, Origin: origin, Timestamp: timestamp, Event: event}
		a.memory = reduce(a.memory, record, a.tailSize)
		a.seen[event.EventID] = struct{}{}
	}
	memory := cloneMemory(a.memory)
	a.mu.Unlock()
	if err := m.save(ctx, memory); err != nil {
		return presencedomain.GroupWorkingMemory{}, err
	}
	return memory, nil
}

// UpdatePromptSession persists the model-visible conversation for one group.
// It is kept behind the same actor lock as event state so prompt history does
// not race with working-memory updates.
func (m *Manager) UpdatePromptSession(ctx context.Context, groupID int64, session conversationdomain.PromptSession) error {
	a, err := m.actor(ctx, groupID)
	if err != nil {
		return err
	}
	return m.save(ctx, a.updatePromptSession(session))
}

func (m *Manager) actor(ctx context.Context, groupID int64) (*actor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("group actor: manager is closed")
	}
	if a := m.groups[groupID]; a != nil {
		return a, nil
	}
	initial := presencedomain.GroupWorkingMemory{GroupID: groupID}
	if m.state != nil {
		loaded, err := m.state.LoadWorkingMemory(ctx, groupID)
		if err != nil {
			return nil, err
		}
		if loaded.GroupID != 0 {
			initial = loaded
		}
	}
	a := newActor(
		groupID,
		m.tailSize,
		0, // maxCandidates removed
		m.maxSeen,
		initial,
		m.decisionEngine,
		m.personaAssembler,
		m.responsePlanner,
		m.responseExecutor,
		m.feedbackCollector,
		m.personaID,
	)
	m.groups[groupID] = a
	return a, nil
}

func (m *Manager) save(ctx context.Context, memory presencedomain.GroupWorkingMemory) error {
	if m.state == nil {
		return nil
	}
	return m.state.SaveWorkingMemory(ctx, memory)
}



// EnqueueCandidate injects proactive or follow-up work into the owning group
// actor. Execution still flows through ClaimDue, deliberation, and action.



// EnrichMedia writes asynchronous perception results through the owning group
// actor. Workers never mutate working memory directly.
func (m *Manager) EnrichMedia(ctx context.Context, groupID int64, eventID string, descriptors []mediadomain.MediaDescriptor) error {
	a, err := m.actor(ctx, groupID)
	if err != nil {
		return err
	}
	return m.save(ctx, a.enrichMedia(eventID, descriptors))
}

func (m *Manager) GroupIDs() []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]int64, 0, len(m.groups))
	for groupID := range m.groups {
		ids = append(ids, groupID)
	}
	return ids
}

// PruneIdle retires actors that have been inactive longer than the configured
// TTL and have no live candidates. Working memory is persisted before the
// actor is dropped from the manager.
func (m *Manager) PruneIdle(ctx context.Context, now time.Time) int {
	if m == nil || m.idleTTL <= 0 {
		return 0
	}
	m.mu.Lock()
	actors := make(map[int64]*actor, len(m.groups))
	for groupID, a := range m.groups {
		actors[groupID] = a
	}
	m.mu.Unlock()

	retired := 0
	for groupID, a := range actors {
		if now.Sub(a.lastUsed()) < m.idleTTL {
			continue
		}
		memory, retire := a.retireIfIdle(now, m.idleTTL)
		if !retire {
			continue
		}
		if err := m.save(ctx, memory); err != nil {
			continue
		}
		m.mu.Lock()
		if m.groups[groupID] == a {
			delete(m.groups, groupID)
			retired++
		}
		m.mu.Unlock()
	}
	return retired
}

func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.mu.Unlock()
	return m.log.Close()
}

// actor holds one group's working memory behind a mutex. Every method takes
// the lock, mutates in place, and returns a detached clone so callers never
// share slices with live state.
type actor struct {
	groupID       int64
	tailSize      int
	maxSeen       int

	mu           sync.Mutex
	memory       presencedomain.GroupWorkingMemory
	seen         map[string]struct{}
	lastUsedNano atomic.Int64

	// 新增：社交决策相关依赖
	decisionEngine    DecisionEngine
	personaAssembler  PersonaAssembler
	responsePlanner   ResponsePlanner
	responseExecutor  ResponseExecutor
	feedbackCollector FeedbackCollector
	personaID         string
}

func newActor(
	groupID int64,
	tailSize, maxCandidates, maxSeen int,
	initial presencedomain.GroupWorkingMemory,
	decisionEngine DecisionEngine,
	personaAssembler PersonaAssembler,
	responsePlanner ResponsePlanner,
	responseExecutor ResponseExecutor,
	feedbackCollector FeedbackCollector,
	personaID string,
) *actor {
	a := &actor{
		groupID:           groupID,
		tailSize:          tailSize,
		// maxCandidates removed
		maxSeen:           maxSeen,
		seen:              make(map[string]struct{}),
		decisionEngine:    decisionEngine,
		personaAssembler:  personaAssembler,
		responsePlanner:   responsePlanner,
		responseExecutor:  responseExecutor,
		feedbackCollector: feedbackCollector,
		personaID:         personaID,
	}
	a.lastUsedNano.Store(time.Now().UnixNano())
	if initial.GroupID == 0 {
		initial.GroupID = groupID
	}
	for _, record := range initial.RecentTail {
		if record.EventID != "" {
			a.seen[record.EventID] = struct{}{}
		}
	}
	a.memory = initial
	return a
}

func nowIdle(lastUsed, now time.Time, ttl time.Duration) bool {
	return ttl > 0 && !lastUsed.IsZero() && now.Sub(lastUsed) >= ttl
}



func (a *actor) touch() { a.lastUsedNano.Store(time.Now().UnixNano()) }

func (a *actor) lastUsed() time.Time {
	return time.Unix(0, a.lastUsedNano.Load())
}

func (a *actor) observe(record presencedomain.EventRecord) presencedomain.GroupWorkingMemory {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.touch()
	if _, duplicate := a.seen[record.EventID]; !duplicate {
		a.seen[record.EventID] = struct{}{}
		a.memory = reduce(a.memory, record, a.tailSize)
		a.pruneSeen(a.memory.RecentTail)
	}

	// 新增：异步调用决策引擎（不阻塞事件记录）
	// 仅处理入站消息
	if record.Origin != presencedomain.OriginOutbound {
		go a.decideAndRespond(context.Background(), &record)
	}
	return cloneMemory(a.memory)
}






func (a *actor) enrichMedia(eventID string, descriptors []mediadomain.MediaDescriptor) presencedomain.GroupWorkingMemory {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.touch()
	enrichMedia(&a.memory, eventID, descriptors)
	return cloneMemory(a.memory)
}

// snapshot must touch the idle clock: deliberation reads it before thinking,
// and a group mid-deliberation must not be reclaimed by PruneIdle.
func (a *actor) snapshot() presencedomain.GroupWorkingMemory {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.touch()
	return cloneMemory(a.memory)
}

func (a *actor) updatePromptSession(session conversationdomain.PromptSession) presencedomain.GroupWorkingMemory {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.touch()
	a.memory.PromptSession = session
	return cloneMemory(a.memory)
}

// retireIfIdle reports whether the actor may be dropped. The check happens
// under the lock so a concurrent touch during the wait is respected.
func (a *actor) retireIfIdle(now time.Time, idleTTL time.Duration) (presencedomain.GroupWorkingMemory, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !nowIdle(a.lastUsed(), now, idleTTL)  {
		return presencedomain.GroupWorkingMemory{}, false
	}
	return cloneMemory(a.memory), true
}

func (a *actor) pruneSeen(tail []presencedomain.EventRecord) {
	if a.maxSeen <= 0 || len(a.seen) <= a.maxSeen {
		return
	}
	seen := make(map[string]struct{}, len(tail))
	for _, record := range tail {
		if record.EventID != "" {
			seen[record.EventID] = struct{}{}
		}
	}
	a.seen = seen
}

func reduce(memory presencedomain.GroupWorkingMemory, record presencedomain.EventRecord, tailSize int) presencedomain.GroupWorkingMemory {
	memory.Version++
	memory.LastUpdatedAt = record.Timestamp
	memory.Checkpoint = presencedomain.ProjectionCheckpoint{
		Name:      "group_working_memory",
		Version:   memory.Version,
		Cursor:    conversationdomain.ContextCursor{EventID: record.EventID, TimestampUnix: record.Timestamp.Unix()},
		UpdatedAt: record.Timestamp,
	}
	memory.RecentTail = append(memory.RecentTail, record)
	if len(memory.RecentTail) > tailSize {
		memory.RecentTail = memory.RecentTail[len(memory.RecentTail)-tailSize:]
	}
	pruneMedia(&memory)

	if record.Origin == presencedomain.OriginOutbound {
		memory.ActiveTopic = strings.TrimSpace(record.Event.Text)
		memory.CurrentBurst = presencedomain.ConversationBurst{}
		return memory
	}
	if record.Event.Kind == conversationdomain.EventMeta {
		return memory
	}

	burst := memory.CurrentBurst
	if !burst.LastAt.IsZero() &&
		record.Timestamp.Sub(burst.LastAt) <= burstWindow &&
		record.Timestamp.Sub(burst.StartedAt) <= burstMaxWindow {
		burst.EventIDs = append(burst.EventIDs, record.EventID)
		burst.Text = strings.TrimSpace(burst.Text + " " + record.Event.Text)
		burst.LastAt = record.Timestamp
		if burst.UserID != record.UserID {
			burst.UserID = 0 // mixed-user burst
		}
	} else {
		burst = presencedomain.ConversationBurst{
			UserID:    record.UserID,
			EventIDs:  []string{record.EventID},
			Text:      strings.TrimSpace(record.Event.Text),
			StartedAt: record.Timestamp,
			LastAt:    record.Timestamp,
		}
	}
	memory.CurrentBurst = burst
	if text := strings.TrimSpace(record.Event.Text); text != "" {
		memory.ActiveTopic = text
		if strings.ContainsAny(text, "?？") {
			memory.OpenLoops = appendUnique(memory.OpenLoops, text)
		}
	}
	return memory
}





// jitteredDelay 在 [lo, hi] 内取均匀随机延迟，让回复节奏有真人式的方差。

// memberJoinCandidate 为入群等群事件生成低优先级招呼时机：想不想欢迎
// 由模型抉择（stay_silent 即安静地无视），所以分数压在阈值边缘。

// pokeCandidate 为被戳事件生成高优先级回应机会。延迟略长于被 @，
// 留出「愣了一下才反应过来」的自然间隔。

// classifyDialogueAct keeps the candidate seam cheap and deterministic while// preserving the user's likely conversational purpose for the planner.
func classifyDialogueAct(text string, direct, hasAttachment bool) (string, float64, string) {
	text = strings.TrimSpace(text)
	if hasAttachment && !direct {
		return "react", 0.5, "media_reaction"
	}
	if direct {
		if containsAny(text, "难受", "好累", "崩溃", "烦死了", "想哭", "不开心") {
			return "support", 1, "direct_distress"
		}
		if containsAny(text, "帮我", "能不能", "可以吗", "请你", "帮忙") {
			return "request_help", 1, "direct_request"
		}
		if containsAny(text, "谢谢", "感谢", "多亏") {
			return "gratitude", 1, "direct_gratitude"
		}
		return "answer", 1, "direct_address"
	}
	if containsAny(text, "难受", "好累", "崩溃", "烦死了", "想哭", "不开心") {
		return "support", 0.65, "distress_observed"
	}
	if containsAny(text, "好无聊", "笑死", "哈哈", "什么鬼", "离谱") {
		return "banter", 0.5, "banter_observed"
	}
	if containsAny(text, "谢谢", "感谢", "多亏") {
		return "gratitude", 0.5, "gratitude_observed"
	}
	if strings.ContainsAny(text, "?？") {
		return "question", 0.6, "question_observed"
	}
	if text == "" {
		return "acknowledge", 0.35, "empty_observed"
	}
	return "continue_topic", 0.35, "topic_continuation"
}

func containsAny(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}


func enrichMedia(memory *presencedomain.GroupWorkingMemory, eventID string, descriptors []mediadomain.MediaDescriptor) {
	if eventID == "" || !eventInTail(memory.RecentTail, eventID) {
		return
	}
	if memory.MediaByEvent == nil {
		memory.MediaByEvent = make(map[string][]mediadomain.MediaDescriptor)
	}
	memory.MediaByEvent[eventID] = append([]mediadomain.MediaDescriptor(nil), descriptors...)
}


func pruneMedia(memory *presencedomain.GroupWorkingMemory) {
	if len(memory.MediaByEvent) == 0 {
		return
	}
	for eventID := range memory.MediaByEvent {
		if !eventInTail(memory.RecentTail, eventID) {
			delete(memory.MediaByEvent, eventID)
		}
	}
}

func eventInTail(records []presencedomain.EventRecord, eventID string) bool {
	return slices.ContainsFunc(records, func(r presencedomain.EventRecord) bool { return r.EventID == eventID })
}


func appendUnique(items []string, value string) []string {
	if slices.Contains(items, value) {
		return items
	}
	return append(items, value)
}


// pruneCandidates bounds long-lived group state. Terminal records are removed
// first; if live work itself exceeds the limit, the newest candidates win so a
// burst does not leave only stale work at the head of the queue.



func cloneMemory(memory presencedomain.GroupWorkingMemory) presencedomain.GroupWorkingMemory {
	memory.RecentTail = append([]presencedomain.EventRecord(nil), memory.RecentTail...)
	memory.OpenLoops = append([]string(nil), memory.OpenLoops...)
	memory.CurrentBurst.EventIDs = append([]string(nil), memory.CurrentBurst.EventIDs...)
	if len(memory.MediaByEvent) > 0 {
		media := make(map[string][]mediadomain.MediaDescriptor, len(memory.MediaByEvent))
		for eventID, descriptors := range memory.MediaByEvent {
			media[eventID] = append([]mediadomain.MediaDescriptor(nil), descriptors...)
		}
		memory.MediaByEvent = media
	}
	memory.PromptSession.Messages = append([]conversationdomain.PromptMessage(nil), memory.PromptSession.Messages...)
	for i := range memory.PromptSession.Messages {
		memory.PromptSession.Messages[i].ToolCalls = append([]conversationdomain.PromptToolCall(nil), memory.PromptSession.Messages[i].ToolCalls...)
	}
	return memory
}

// decideAndRespond 使用决策引擎判断是否参与并执行回复
func (a *actor) decideAndRespond(ctx context.Context, evt *presencedomain.EventRecord) {
	// 仅当所有依赖都配置时才执行新流程
	if a.decisionEngine == nil || a.personaAssembler == nil || 
	   a.responsePlanner == nil || a.responseExecutor == nil {
		return
	}

	// 1. 构建决策请求
	req := socialdecisionsvc.DecisionRequest{
		PersonaID:      a.personaID,
		GroupID:        evt.GroupID,
		TriggerEventID: evt.EventID,
		TargetUserID:   evt.UserID,
		
		// 硬规则检查
		IsSelfMessage:              evt.Origin == presencedomain.OriginOutbound,
		SecondsSinceLastBotMessage: a.getSecondsSinceLastBot(),
		ConsecutiveBotMessages:     a.getConsecutiveBotCount(),
		EventAge:                   time.Since(time.Unix(evt.Event.TimestampUnix, 0)),
		
		// 场景检查
		IsDirectMention:    evt.Event.MentionedBot || evt.Event.NamedBot,
		IsQuestionToBot:    a.isQuestionToBot(evt),
		IsFastConversation: a.isFastConversation(),
	}
	
	// 2. 调用决策引擎
	decision, err := a.decisionEngine.DecideParticipation(ctx, req)
	if err != nil {
		// 决策失败不阻塞，仅记录
		return
	}
	
	// 3. 如果决定不参与，直接返回
	if !decision.Participate {
		return
	}
	
	// 4. 组装 PersonaContext
	personaCtx, err := a.personaAssembler.AssembleContext(ctx, 
		personadomain.PersonaConfig{ID: a.personaID}, 
		evt.GroupID)
	if err != nil {
		return
	}
	
	// 5. 创建回复计划
	convEvt := a.toConversationEvent(evt)
	plan, err := a.responsePlanner.CreateResponsePlan(ctx, decision, convEvt, personaCtx)
	if err != nil {
		return
	}
	
	// 6. 执行回复计划
	actionID, err := a.responseExecutor.ExecutePlan(ctx, plan)
	if err != nil {
		return
	}
	
	// 7. 启动反馈窗口（异步）
	if a.feedbackCollector != nil {
		go a.startFeedbackWindow(ctx, decision.DecisionID, actionID, evt.GroupID)
	}
}

// getSecondsSinceLastBot 获取距离上次 bot 发言的秒数
func (a *actor) getSecondsSinceLastBot() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	
	for i := len(a.memory.RecentTail) - 1; i >= 0; i-- {
		evt := a.memory.RecentTail[i]
		if evt.Origin == presencedomain.OriginOutbound {
			elapsed := time.Since(time.Unix(evt.Event.TimestampUnix, 0))
			return int(elapsed.Seconds())
		}
	}
	return 999999 // 很久没说话
}

// getConsecutiveBotCount 获取连续 bot 发言次数
func (a *actor) getConsecutiveBotCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	
	count := 0
	for i := len(a.memory.RecentTail) - 1; i >= 0; i-- {
		evt := a.memory.RecentTail[i]
		if evt.Origin == presencedomain.OriginOutbound {
			count++
		} else {
			break
		}
	}
	return count
}

// isQuestionToBot 判断是否是对 bot 的提问
func (a *actor) isQuestionToBot(evt *presencedomain.EventRecord) bool {
	text := strings.ToLower(evt.Event.Text)
	return strings.Contains(text, "?") || 
	       strings.Contains(text, "？") ||
	       strings.HasPrefix(text, "为什么") ||
	       strings.HasPrefix(text, "怎么") ||
	       strings.HasPrefix(text, "什么")
}

// isFastConversation 判断是否是快速对话
func (a *actor) isFastConversation() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	
	if len(a.memory.RecentTail) < 3 {
		return false
	}
	
	// 检查最近3条消息是否在5秒内
	tail := a.memory.RecentTail
	last := tail[len(tail)-1]
	third := tail[len(tail)-3]
	gap := time.Unix(last.Event.TimestampUnix, 0).Sub(time.Unix(third.Event.TimestampUnix, 0))
	return gap < 5*time.Second
}

// toConversationEvent 转换为 ConversationEvent
func (a *actor) toConversationEvent(evt *presencedomain.EventRecord) *conversationdomain.ConversationEvent {
	return &conversationdomain.ConversationEvent{
		EventID:       evt.EventID,
		GroupID:       evt.GroupID,
		UserID:        evt.UserID,
		MessageID:     evt.Event.MessageID,
		Text:          evt.Event.Text,
		MentionedBot:  evt.Event.MentionedBot,
		NamedBot:      evt.Event.NamedBot,
		IsReplyToBot:  evt.Event.IsReplyToBot,
		TimestampUnix: evt.Event.TimestampUnix,
	}
}

// startFeedbackWindow 启动反馈窗口
func (a *actor) startFeedbackWindow(
	ctx context.Context, 
	decisionID, actionID string, 
	groupID int64,
) {
	// 启动反馈窗口
	window, err := a.feedbackCollector.StartFeedbackWindow(
		ctx,
		decisionID,
		actionID,
		groupID,
		time.Now(),
	)
	if err != nil {
		return
	}
	
	// 等待观察期结束
	time.Sleep(window.ObserveDuration)
	
	// 收集反馈
	feedback, err := a.feedbackCollector.CollectFeedback(ctx, window)
	if err != nil {
		return
	}
	
	// TODO: 根据反馈更新人格状态
	_ = feedback
}
