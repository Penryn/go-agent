// Package runtime owns the human-presence message lifecycle.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/phlin/go-agent/internal/application/action"
	normalizersvc "github.com/phlin/go-agent/internal/application/normalizer"
	personasvc "github.com/phlin/go-agent/internal/application/persona"
	"github.com/phlin/go-agent/internal/application/ports"
	"github.com/phlin/go-agent/internal/application/presence/deliberation"
	groupactor "github.com/phlin/go-agent/internal/application/presence/group_actor"
	retrievalsvc "github.com/phlin/go-agent/internal/application/retrieval"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

type Config struct {
	JobTimeout     time.Duration
	GroupWhitelist []int64
	SelfID         int64
	// ProactiveInterval 是主动开口扫描周期；0 时禁用主动发言。
	ProactiveInterval time.Duration
	// ProactiveBaseProbability 是冷场开口的基础概率（0~1）。
	ProactiveBaseProbability float64
	// ProactiveScoreThreshold 是主动候选的评分门槛（0~1），低于此值不开口。
	ProactiveScoreThreshold float64
}

type PerceptionSubmitter interface {
	Submit(presencedomain.EventRecord)
}

type ConfirmationObserver interface {
	ObserveConfirmation(groupID, userID int64, text string, at time.Time)
}

// EventObserverFunc 在每条入站事件后收到回调；错误只记日志。
type EventObserverFunc func(context.Context, conversationdomain.ConversationEvent) error

// CompletedTurnObserverFunc 在一轮回复完成后收到回调；错误只记日志。
type CompletedTurnObserverFunc func(context.Context, conversationdomain.ContextSnapshot, replydomain.ActionReceipt) error

// TurnObserver closes the feedback loop between a realized action and future
// scheduling. Implementations own durable cooldown, persona, and reflection
// state; Runtime only invokes the narrow lifecycle hooks.
type TurnObserver interface {
	CanDeliberate(context.Context, int64, time.Time) (bool, error)
	AfterTurn(context.Context, conversationdomain.ContextSnapshot, policydomain.AutonomyDecision, replydomain.ActionReceipt) error
}

// InboundTurnObserver is an optional lifecycle hook for observers that keep
// state about consecutive bot turns. It is separate from TurnObserver so
// existing lightweight test and embedding implementations remain compatible.
type InboundTurnObserver interface {
	ObserveInbound(context.Context, conversationdomain.ConversationEvent) error
}

// pollInterval 是调度循环周期;100ms 足够即时,再快只会空转。
const pollInterval = 100 * time.Millisecond
const maxConcurrentModelRuns = 4

func DefaultConfig() Config {
	return Config{JobTimeout: 120 * time.Second}
}

// 主动开口的触发条件：群冷场超过该时长，且群里有未接上的话题（OpenLoops）
// 或 bot 自己感兴趣的话题残留（ActiveTopic）。
const (
	proactiveIdleThreshold = 3 * time.Minute
	// 主动候选的延迟区间：冷场后不马上冒头，等一小段再自然开口。
	proactiveDelayMin = 30 * time.Second
	proactiveDelayMax = 3 * time.Minute
	// 主动候选过期窗口。
	proactiveTTL = 10 * time.Minute
	// 同一群两次主动开口的最小间隔。
	proactiveGroupCooldown = 20 * time.Minute
)

type Outcome struct {
	Envelope  conversationdomain.EventEnvelope   `json:"envelope"`
	Snapshot  conversationdomain.ContextSnapshot `json:"snapshot"`
	Candidate interface{}                        `json:"candidate"` // 已废弃，保留以保持接口兼容
	Decision  policydomain.AutonomyDecision      `json:"decision"`
	Plan      replydomain.ReplyPlan              `json:"plan"`
	Receipt   replydomain.ActionReceipt          `json:"receipt"`
}

type Runtime struct {
	normalizer    *normalizersvc.Service
	working       *groupactor.Manager
	deliberator   deliberation.Deliberator
	perception    PerceptionSubmitter
	confirmations ConfirmationObserver
	turns         TurnObserver
	canon         *personasvc.CanonService
	executor      *action.Service
	thoughts      ports.ThoughtStore
	// retriever 供主动开口时从长期记忆挑旧梗；nil 时跳过。
	retriever              *retrievalsvc.Service
	eventObservers         []EventObserverFunc
	completedTurnObservers []CompletedTurnObserverFunc
	whitelist              map[int64]struct{}
	selfID                 int64
	proactiveInterval      time.Duration
	proactiveProbability   float64
	proactiveThreshold     float64
	lastProactive          map[int64]time.Time
	proactiveMu            sync.Mutex
	groupRunMu             sync.Mutex
	groupRunLocks          map[int64]*sync.Mutex
	modelSlots             chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// SetThoughtStore enables durable, concise deliberation records. It is
// optional so replay and in-memory callers can keep the runtime lightweight.
func (r *Runtime) SetThoughtStore(store ports.ThoughtStore) { r.thoughts = store }

// SetMemoryRetriever enables proactive memory recall during idle revival.
// Optional; nil keeps the proactive loop limited to OpenLoops / ActiveTopic.
func (r *Runtime) SetMemoryRetriever(retriever *retrievalsvc.Service) { r.retriever = retriever }

func (r *Runtime) SetConfirmationObserver(observer ConfirmationObserver) { r.confirmations = observer }

// SetCanonService enables post-delivery persistence for fictional persona
// facts declared in terminal reply tools.
func (r *Runtime) SetCanonService(service *personasvc.CanonService) { r.canon = service }

func (r *Runtime) AddEventObserver(observer EventObserverFunc) {
	if observer != nil {
		r.eventObservers = append(r.eventObservers, observer)
	}
}

func (r *Runtime) AddCompletedTurnObserver(observer CompletedTurnObserverFunc) {
	if observer != nil {
		r.completedTurnObservers = append(r.completedTurnObservers, observer)
	}
}

func New(parent context.Context, normalizer *normalizersvc.Service, working *groupactor.Manager, deliberator deliberation.Deliberator, perception PerceptionSubmitter, turns TurnObserver, executor *action.Service, cfg Config) *Runtime {
	defaults := DefaultConfig()
	if cfg.JobTimeout <= 0 {
		cfg.JobTimeout = defaults.JobTimeout
	}
	ctx, cancel := context.WithCancel(parent)
	r := &Runtime{
		normalizer:           normalizer,
		working:              working,
		deliberator:          deliberator,
		perception:           perception,
		turns:                turns,
		executor:             executor,
		whitelist:            make(map[int64]struct{}, len(cfg.GroupWhitelist)),
		selfID:               cfg.SelfID,
		proactiveInterval:    cfg.ProactiveInterval,
		proactiveProbability: cfg.ProactiveBaseProbability,
		proactiveThreshold:   cfg.ProactiveScoreThreshold,
		lastProactive:        make(map[int64]time.Time),
		groupRunLocks:        make(map[int64]*sync.Mutex),
		modelSlots:           make(chan struct{}, maxConcurrentModelRuns),
		ctx:                  ctx,
		cancel:               cancel,
	}
	for _, groupID := range cfg.GroupWhitelist {
		r.whitelist[groupID] = struct{}{}
	}
	r.wg.Add(1)
	go r.loop(pollInterval, cfg.JobTimeout)
	if r.proactiveInterval > 0 && r.proactiveProbability > 0 {
		r.wg.Add(1)
		go r.proactiveLoop()
	}
	return r
}

// SubmitRaw records an event and returns immediately. Deliberation is driven
// by the scheduler, so slow models never block the inbound reader.
func (r *Runtime) SubmitRaw(ctx context.Context, payload []byte) error {
	envelope, err := r.normalize(payload)
	if err != nil {
		return err
	}
	if r.shouldIgnore(envelope) {
		return nil
	}
	if r.executor != nil {
		r.executor.CancelQueued(envelope.Event.GroupID)
	}
	if observer, ok := r.turns.(InboundTurnObserver); ok {
		if err := observer.ObserveInbound(ctx, envelope.Event); err != nil {
			return fmt.Errorf("observe inbound turn: %w", err)
		}
	}
	if r.confirmations != nil {
		r.confirmations.ObserveConfirmation(envelope.Event.GroupID, envelope.Event.UserID, envelope.Event.Text, envelope.ReceivedAt)
	}
	record := toEventRecord(envelope, presencedomain.OriginInbound)
	_, err = r.working.Observe(ctx, record)
	if err == nil && r.perception != nil {
		r.perception.Submit(record)
	}
	if err == nil {
		r.observeEvent(ctx, envelope.Event)
	}
	return err
}

// ScheduleCandidate is the runtime seam for proactive work. The candidate
// enters the same group mailbox as inbound-derived thoughts and receives the
// same staleness, throttling, and action validation.
// 注意：此方法已废弃，决策现在由 group_actor 的决策引擎处理
func (r *Runtime) ScheduleCandidate(ctx context.Context, groupID int64, candidate interface{}) error {
	// No-op: 候选系统已被决策引擎替代
	return nil
}

// ProcessRawEvent is the synchronous replay surface. It records the event,
// then immediately deliberates its highest-value candidate for CLI/tests.
// 注意：候选逻辑已被决策引擎替代，此方法现在仅记录事件
func (r *Runtime) ProcessRawEvent(ctx context.Context, payload []byte) (Outcome, error) {
	envelope, err := r.normalize(payload)
	if err != nil {
		return Outcome{}, fmt.Errorf("normalize event: %w", err)
	}
	if r.shouldIgnore(envelope) {
		return Outcome{Envelope: envelope, Decision: silentDecision(envelope.TraceID, "ignored")}, nil
	}
	if observer, ok := r.turns.(InboundTurnObserver); ok {
		if err := observer.ObserveInbound(ctx, envelope.Event); err != nil {
			return Outcome{Envelope: envelope}, fmt.Errorf("observe inbound turn: %w", err)
		}
	}
	record := toEventRecord(envelope, presencedomain.OriginInbound)
	_, err = r.working.Observe(ctx, record)
	if err != nil {
		return Outcome{}, fmt.Errorf("observe event: %w", err)
	}
	if r.perception != nil {
		r.perception.Submit(record)
	}
	if r.confirmations != nil {
		r.confirmations.ObserveConfirmation(envelope.Event.GroupID, envelope.Event.UserID, envelope.Event.Text, envelope.ReceivedAt)
	}
	r.observeEvent(ctx, envelope.Event)

	// 决策现在由 group_actor 的 decideAndRespond 处理
	return Outcome{Envelope: envelope, Decision: silentDecision(envelope.TraceID, "delegated_to_decision_engine")}, nil
}

func (r *Runtime) loop(interval, timeout time.Duration) {
	defer r.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case now := <-ticker.C:
			r.working.PruneIdle(r.ctx, now)
			// 候选轮询已被决策引擎替代
		}
	}
}

// runCandidate 已废弃 - 决策现在由 group_actor 的决策引擎处理
func (r *Runtime) runCandidate(groupID int64, candidate interface{}, timeout time.Duration) {
	defer r.wg.Done()
	// No-op: 候选处理已被决策引擎替代
}

func (r *Runtime) groupRunLock(groupID int64) *sync.Mutex {
	r.groupRunMu.Lock()
	defer r.groupRunMu.Unlock()
	if r.groupRunLocks == nil {
		r.groupRunLocks = make(map[int64]*sync.Mutex)
	}
	if lock := r.groupRunLocks[groupID]; lock != nil {
		return lock
	}
	lock := &sync.Mutex{}
	r.groupRunLocks[groupID] = lock
	return lock
}

// proactiveLoop 是主动开口扫描器：群冷场且仍有未接话题时，以配置的
// 基础概率投递一个低分长延迟的主动 candidate。是否真的说出来仍由
// 模型抉择共同决定——这里只制造机会。
func (r *Runtime) proactiveLoop() {
	defer r.wg.Done()
	ticker := time.NewTicker(r.proactiveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case now := <-ticker.C:
			r.scanProactive(now)
		}
	}
}

func (r *Runtime) scanProactive(now time.Time) {
	for _, groupID := range r.working.GroupIDs() {
		if r.lastProactiveAt(groupID).After(now.Add(-proactiveGroupCooldown)) {
			continue
		}
		// 主动发言循环已废弃 - 现在由 group_actor 的决策引擎处理
		_ = groupID
		_ = now
		/*
		candidate, ok := r.proactiveCandidate(groupID, now)
		if !ok {
			continue
		}
		if err := r.working.EnqueueCandidate(r.ctx, groupID, candidate); err != nil {
			slog.Debug("human runtime: proactive enqueue failed", "group_id", groupID, "err", err)
			continue
		}
		r.markProactive(groupID, now)
		slog.Info("human runtime: proactive candidate enqueued",
			"group_id", groupID, "topic", candidate.TopicID, "score", candidate.Score)
		*/
	}
}

// proactiveCandidate 判断一群是否值得主动开口并生成候选。
// 话题素材优先取未接话题（OpenLoops），其次从长期记忆挑一条高分旧事——
// 「我记得你上次说过XX」的主动回忆。没到阈值就不开口。
// proactiveCandidate 已废弃 - 主动发言现在由 group_actor 的决策引擎处理
func (r *Runtime) proactiveCandidate(groupID int64, now time.Time) (interface{}, bool) {
	return nil, false
}

// recallWorthyMemory 从长期记忆里挑一条适合冷场提起的旧事。
// 只取重要性 >= 0.6 的（低分闲事硬提会显得奇怪），失败静默。
func (r *Runtime) recallWorthyMemory(groupID int64) string {
	if r.retriever == nil {
		return ""
	}
	records, err := r.retriever.SearchMemories(r.ctx, ports.MemoryQuery{
		GroupID: groupID,
		TopK:    3,
	})
	if err != nil {
		return ""
	}
	for _, record := range records {
		if record.Importance >= 0.6 && strings.TrimSpace(record.Content) != "" {
			return record.Content
		}
	}
	return ""
}

func (r *Runtime) lastProactiveAt(groupID int64) time.Time {
	r.proactiveMu.Lock()
	defer r.proactiveMu.Unlock()
	return r.lastProactive[groupID]
}

func (r *Runtime) markProactive(groupID int64, now time.Time) {
	r.proactiveMu.Lock()
	defer r.proactiveMu.Unlock()
	r.lastProactive[groupID] = now
}

// processCandidate 已废弃 - 决策现在由 group_actor 的决策引擎处理
func (r *Runtime) processCandidate(ctx context.Context, groupID int64, candidate interface{}) (Outcome, error) {
	return Outcome{Decision: silentDecision("", "delegated_to_decision_engine")}, nil
}

func (r *Runtime) process(ctx context.Context, envelope conversationdomain.EventEnvelope, candidate interface{}) (Outcome, error) {
	return r.processWithValidation(ctx, envelope, candidate, nil)
}

func (r *Runtime) processWithValidation(ctx context.Context, envelope conversationdomain.EventEnvelope, candidate interface{}, validate func(context.Context) (bool, error)) (Outcome, error) {
	// 候选处理已被决策引擎替代
	return Outcome{Envelope: envelope, Decision: silentDecision(envelope.TraceID, "delegated_to_decision_engine")}, nil
}

func (r *Runtime) silentBeforeModel(ctx context.Context, envelope conversationdomain.EventEnvelope, candidate interface{}, reason string) (Outcome, error) {
	decision := silentDecision(envelope.TraceID, reason)
	plan := replydomain.ReplyPlan{PlanID: decision.DecisionID + "-plan", PlannedActions: []policydomain.DecisionAction{policydomain.ActionSilent}, SendMode: "silent"}
	var receipt replydomain.ActionReceipt
	var err error
	if r.executor != nil {
		receipt, err = r.executor.Execute(ctx, envelope.Event, decision, plan)
	} else {
		receipt.DropReason = "action_silent"
	}
	return Outcome{Envelope: envelope, Decision: decision, Plan: plan, Receipt: receipt}, err
}

func plannedReplyText(plan replydomain.ReplyPlan) string {
	if len(plan.Bubbles) > 0 {
		return strings.Join(plan.Bubbles, "")
	}
	if corrected, _ := plan.ActionParams["corrected_text"].(string); corrected != "" {
		return corrected
	}
	return plan.FallbackText
}

func (r *Runtime) observeEvent(ctx context.Context, event conversationdomain.ConversationEvent) {
	for _, observer := range r.eventObservers {
		if err := observer(ctx, event); err != nil {
			slog.Warn("human runtime: event observer failed", "group_id", event.GroupID, "user_id", event.UserID, "err", err)
		}
	}
}

func (r *Runtime) normalize(payload []byte) (conversationdomain.EventEnvelope, error) {
	if r.normalizer == nil {
		return conversationdomain.EventEnvelope{}, errors.New("human runtime: normalizer is nil")
	}
	return r.normalizer.Normalize(payload)
}

func (r *Runtime) shouldIgnore(envelope conversationdomain.EventEnvelope) bool {
	if envelope.Event.Kind == conversationdomain.EventMeta || envelope.Event.GroupID <= 0 {
		return true
	}
	if envelope.Event.UserID != 0 && envelope.Event.UserID == envelope.SelfID {
		return true
	}
	if len(r.whitelist) == 0 {
		return false
	}
	_, ok := r.whitelist[envelope.Event.GroupID]
	return !ok
}

func toEventRecord(envelope conversationdomain.EventEnvelope, origin presencedomain.EventOrigin) presencedomain.EventRecord {
	return presencedomain.EventRecord{EventID: envelope.Event.EventID, GroupID: envelope.Event.GroupID, UserID: envelope.Event.UserID, Origin: origin, Timestamp: envelope.ReceivedAt, Event: envelope.Event, RawPayload: envelope.RawPayload}
}

func silentDecision(id, reason string) policydomain.AutonomyDecision {
	return policydomain.AutonomyDecision{DecisionID: id + "-decision", Action: policydomain.ActionSilent, ReasonCodes: []string{reason}, Confidence: 1}
}

func (r *Runtime) Close() error {
	r.cancel()
	r.wg.Wait()
	return nil
}
