package group_actor

import (
	"context"
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
	"github.com/phlin/go-agent/internal/humanbot/runtime/ingress"
)

const defaultTailSize = 32
const defaultMaxCandidates = 256
const defaultMaxSeen = 2048
const burstWindow = 2 * time.Second

type Manager struct {
	log           ingress.EventLog
	archive       ports.MemoryStore
	state         WorkingMemoryStore
	tailSize      int
	maxCandidates int
	maxSeen       int
	idleTTL       time.Duration

	mu     sync.Mutex
	closed bool
	groups map[int64]*actor
}

type Option func(*Manager)

// WorkingMemoryStore persists the actor's rebuildable per-group projection.
type WorkingMemoryStore interface {
	LoadWorkingMemory(context.Context, int64) (humandomain.GroupWorkingMemory, error)
	SaveWorkingMemory(context.Context, humandomain.GroupWorkingMemory) error
}

func WithTailSize(size int) Option {
	return func(m *Manager) {
		if size > 0 {
			m.tailSize = size
		}
	}
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

func NewManager(log ingress.EventLog, opts ...Option) *Manager {
	m := &Manager{log: log, tailSize: defaultTailSize, maxCandidates: defaultMaxCandidates, maxSeen: defaultMaxSeen, groups: make(map[int64]*actor)}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Observe durably records the fact before handing it to the group actor. The
// actor only performs fast state updates, so perception does not wait on an LLM.
func (m *Manager) Observe(ctx context.Context, record humandomain.EventRecord) (humandomain.GroupWorkingMemory, error) {
	if m == nil || m.log == nil {
		return humandomain.GroupWorkingMemory{}, errors.New("group actor: event log is nil")
	}
	if record.EventID == "" {
		return humandomain.GroupWorkingMemory{}, errors.New("group actor: event id is required")
	}
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}
	if record.Origin == "" {
		record.Origin = humandomain.OriginInbound
	}
	// The durable archive is the source of truth for received facts. Write it
	// before advancing the in-memory deduplication cursor so a transient store
	// failure can be retried with the same event ID.
	if m.archive != nil {
		if err := m.archive.ArchiveEvent(ctx, record.Event); err != nil {
			return humandomain.GroupWorkingMemory{}, err
		}
	}

	if dedup, ok := m.log.(ingress.DeduplicatingEventLog); ok {
		var err error
		_, err = dedup.AppendIfNew(ctx, record)
		if err != nil {
			return humandomain.GroupWorkingMemory{}, err
		}
	} else if err := m.log.Append(ctx, record); err != nil {
		return humandomain.GroupWorkingMemory{}, err
	}
	a, err := m.actor(ctx, record.GroupID)
	if err != nil {
		return humandomain.GroupWorkingMemory{}, err
	}
	memory := a.observe(record)
	if err := m.save(ctx, memory); err != nil {
		return memory, err
	}
	return memory, nil
}

func (m *Manager) Snapshot(ctx context.Context, groupID int64) (humandomain.GroupWorkingMemory, error) {
	a, err := m.actor(ctx, groupID)
	if err != nil {
		return humandomain.GroupWorkingMemory{}, err
	}
	return a.snapshot(), nil
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
	initial := humandomain.GroupWorkingMemory{GroupID: groupID}
	if m.state != nil {
		loaded, err := m.state.LoadWorkingMemory(ctx, groupID)
		if err != nil {
			return nil, err
		}
		if loaded.GroupID != 0 {
			initial = loaded
		}
	}
	a := newActor(groupID, m.tailSize, m.maxCandidates, m.maxSeen, initial)
	m.groups[groupID] = a
	return a, nil
}

func (m *Manager) save(ctx context.Context, memory humandomain.GroupWorkingMemory) error {
	if m.state == nil {
		return nil
	}
	return m.state.SaveWorkingMemory(ctx, memory)
}

func (m *Manager) ClaimDue(ctx context.Context, groupID int64, now time.Time, minScore float64) (humandomain.ThoughtCandidate, bool, error) {
	a, err := m.actor(ctx, groupID)
	if err != nil {
		return humandomain.ThoughtCandidate{}, false, err
	}
	candidate, memory := a.claim(now, minScore)
	if candidate == nil {
		return humandomain.ThoughtCandidate{}, false, nil
	}
	if err := m.save(ctx, memory); err != nil {
		return humandomain.ThoughtCandidate{}, false, err
	}
	return *candidate, true, nil
}

// EnqueueCandidate injects proactive or follow-up work into the owning group
// actor. Execution still flows through ClaimDue, deliberation, and action.
func (m *Manager) EnqueueCandidate(ctx context.Context, groupID int64, candidate humandomain.ThoughtCandidate) error {
	if candidate.CandidateID == "" {
		return errors.New("group actor: candidate id is required")
	}
	if candidate.Status == "" {
		candidate.Status = humandomain.CandidatePending
	}
	if candidate.DeliveryTarget == "" {
		candidate.DeliveryTarget = "group"
	}
	if candidate.DueAt.IsZero() {
		candidate.DueAt = time.Now()
	}
	if candidate.ExpiresAt.IsZero() {
		candidate.ExpiresAt = candidate.DueAt.Add(10 * time.Minute)
	}
	if candidate.Score == 0 {
		candidate.Score = candidate.Urgency
	}
	a, err := m.actor(ctx, groupID)
	if err != nil {
		return err
	}
	memory, ok := a.enqueue(candidate)
	if !ok {
		return errors.New("group actor: candidate already exists")
	}
	return m.save(ctx, memory)
}

func (m *Manager) Complete(ctx context.Context, groupID int64, candidateID string) error {
	a, err := m.actor(ctx, groupID)
	if err != nil {
		return err
	}
	return m.save(ctx, a.complete(candidateID))
}

// CanExecute confirms that an accepted candidate has not been superseded or
// expired while a worker was waiting on group serialization or model latency.
func (m *Manager) CanExecute(ctx context.Context, groupID int64, candidateID string, now time.Time) (bool, error) {
	a, err := m.actor(ctx, groupID)
	if err != nil {
		return false, err
	}
	valid, memory := a.canExecute(candidateID, now)
	if err := m.save(ctx, memory); err != nil {
		return false, err
	}
	return valid, nil
}

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
	if closer, ok := m.log.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

// actor holds one group's working memory behind a mutex. Every method takes
// the lock, mutates in place, and returns a detached clone so callers never
// share slices with live state.
type actor struct {
	groupID       int64
	tailSize      int
	maxCandidates int
	maxSeen       int

	mu           sync.Mutex
	memory       humandomain.GroupWorkingMemory
	seen         map[string]struct{}
	lastUsedNano atomic.Int64
}

func newActor(groupID int64, tailSize, maxCandidates, maxSeen int, initial humandomain.GroupWorkingMemory) *actor {
	a := &actor{
		groupID:       groupID,
		tailSize:      tailSize,
		maxCandidates: maxCandidates,
		maxSeen:       maxSeen,
		seen:          make(map[string]struct{}),
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

func hasLiveCandidates(candidates []humandomain.ThoughtCandidate) bool {
	for _, candidate := range candidates {
		if isLive(candidate.Status) {
			return true
		}
	}
	return false
}

func isLive(status humandomain.CandidateStatus) bool {
	return status == humandomain.CandidatePending || status == humandomain.CandidateDeferred || status == humandomain.CandidateAccepted
}

func (a *actor) touch() { a.lastUsedNano.Store(time.Now().UnixNano()) }

func (a *actor) lastUsed() time.Time {
	return time.Unix(0, a.lastUsedNano.Load())
}

func (a *actor) observe(record humandomain.EventRecord) humandomain.GroupWorkingMemory {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.touch()
	if _, duplicate := a.seen[record.EventID]; !duplicate {
		a.seen[record.EventID] = struct{}{}
		a.memory = reduce(a.memory, record, a.tailSize)
		pruneCandidates(&a.memory, a.maxCandidates)
		a.pruneSeen(a.memory.RecentTail)
	}
	return cloneMemory(a.memory)
}

func (a *actor) claim(now time.Time, minScore float64) (*humandomain.ThoughtCandidate, humandomain.GroupWorkingMemory) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.touch()
	candidate := claimCandidate(&a.memory, now, minScore)
	pruneCandidates(&a.memory, a.maxCandidates)
	return candidate, cloneMemory(a.memory)
}

func (a *actor) enqueue(candidate humandomain.ThoughtCandidate) (humandomain.GroupWorkingMemory, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.touch()
	if !enqueueCandidate(&a.memory, candidate) {
		return humandomain.GroupWorkingMemory{}, false
	}
	pruneCandidates(&a.memory, a.maxCandidates)
	return cloneMemory(a.memory), true
}

func (a *actor) complete(candidateID string) humandomain.GroupWorkingMemory {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.touch()
	completeCandidate(&a.memory, candidateID)
	pruneCandidates(&a.memory, a.maxCandidates)
	return cloneMemory(a.memory)
}

func (a *actor) canExecute(candidateID string, now time.Time) (bool, humandomain.GroupWorkingMemory) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.touch()
	valid := canExecuteCandidate(&a.memory, candidateID, now)
	return valid, cloneMemory(a.memory)
}

func (a *actor) enrichMedia(eventID string, descriptors []mediadomain.MediaDescriptor) humandomain.GroupWorkingMemory {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.touch()
	enrichMedia(&a.memory, eventID, descriptors)
	return cloneMemory(a.memory)
}

// snapshot must touch the idle clock: deliberation reads it before thinking,
// and a group mid-deliberation must not be reclaimed by PruneIdle.
func (a *actor) snapshot() humandomain.GroupWorkingMemory {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.touch()
	return cloneMemory(a.memory)
}

// retireIfIdle reports whether the actor may be dropped. The check happens
// under the lock so a concurrent touch during the wait is respected.
func (a *actor) retireIfIdle(now time.Time, idleTTL time.Duration) (humandomain.GroupWorkingMemory, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !nowIdle(a.lastUsed(), now, idleTTL) || hasLiveCandidates(a.memory.Candidates) {
		return humandomain.GroupWorkingMemory{}, false
	}
	return cloneMemory(a.memory), true
}

func (a *actor) pruneSeen(tail []humandomain.EventRecord) {
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

func reduce(memory humandomain.GroupWorkingMemory, record humandomain.EventRecord, tailSize int) humandomain.GroupWorkingMemory {
	memory.Version++
	memory.LastUpdatedAt = record.Timestamp
	memory.RecentTail = append(memory.RecentTail, record)
	if len(memory.RecentTail) > tailSize {
		memory.RecentTail = memory.RecentTail[len(memory.RecentTail)-tailSize:]
	}
	pruneMedia(&memory)

	if record.Origin == humandomain.OriginOutbound {
		memory.ActiveTopic = strings.TrimSpace(record.Event.Text)
		memory.CurrentBurst = humandomain.ConversationBurst{}
		return memory
	}
	if record.Event.Kind == conversationdomain.EventMeta {
		return memory
	}
	// 被戳是强互动信号：真人几乎必回（哪怕只是一句抱怨），单独生成
	// 高优先级 candidate，不进 burst 合并。
	if record.Event.Kind == conversationdomain.EventPoke {
		memory.Candidates = append(memory.Candidates, pokeCandidate(record))
		return memory
	}
	// notice 事件（新成员进群等）：低优先级打个招呼的时机。没有文本，
	// 之前会掉进空文本分支被丢弃——真人对新人进群常会接一句。
	if record.Event.Kind == conversationdomain.EventNotice && record.UserID != 0 {
		memory.Candidates = append(memory.Candidates, memberJoinCandidate(record))
		return memory
	}

	burst := memory.CurrentBurst
	if burst.UserID == record.UserID && !burst.LastAt.IsZero() && record.Timestamp.Sub(burst.LastAt) <= burstWindow {
		burst.EventIDs = append(burst.EventIDs, record.EventID)
		burst.Text = strings.TrimSpace(burst.Text + " " + record.Event.Text)
		burst.LastAt = record.Timestamp
	} else {
		burst = humandomain.ConversationBurst{
			UserID:    record.UserID,
			EventIDs:  []string{record.EventID},
			Text:      strings.TrimSpace(record.Event.Text),
			StartedAt: record.Timestamp,
			LastAt:    record.Timestamp,
		}
	}
	memory.CurrentBurst = burst
	supersedeBurstCandidates(&memory, burst.EventIDs)
	if text := strings.TrimSpace(record.Event.Text); text != "" {
		memory.ActiveTopic = text
		if strings.ContainsAny(text, "?？") {
			memory.OpenLoops = appendUnique(memory.OpenLoops, text)
		}
	}
	memory.Candidates = append(memory.Candidates, candidateFor(record, burst))
	return memory
}

func supersedeBurstCandidates(memory *humandomain.GroupWorkingMemory, eventIDs []string) {
	if len(eventIDs) < 2 {
		return
	}
	for i := range memory.Candidates {
		candidate := &memory.Candidates[i]
		if candidate.Status != humandomain.CandidatePending && candidate.Status != humandomain.CandidateDeferred && candidate.Status != humandomain.CandidateAccepted {
			continue
		}
		if overlaps(candidate.SourceEventIDs, eventIDs[:len(eventIDs)-1]) {
			candidate.Status = humandomain.CandidateCancelled
		}
	}
}

func overlaps(left, right []string) bool {
	for _, l := range left {
		if slices.Contains(right, l) {
			return true
		}
	}
	return false
}

func candidateFor(record humandomain.EventRecord, burst humandomain.ConversationBurst) humandomain.ThoughtCandidate {
	direct := record.Event.MentionedBot || record.Event.NamedBot || record.Event.IsReplyToBot
	intent, urgency, reason := classifyDialogueAct(record.Event.Text, direct, len(record.Event.Attachments) > 0)
	// 回复延迟带方差：被直接 cue 时 0.8~2.5s（像顺手回一句），主动接话
	// 3~12s（像犹豫了一下才决定插嘴）。固定延迟是自动回复最明显的破绽。
	delay := jitteredDelay(3*time.Second, 12*time.Second)
	if direct {
		delay = jitteredDelay(800*time.Millisecond, 2500*time.Millisecond)
	}
	return humandomain.ThoughtCandidate{
		CandidateID:    record.EventID + "-candidate",
		SourceEventIDs: append([]string(nil), burst.EventIDs...),
		TopicID:        memoryTopic(record.Event.Text),
		Addressee:      record.UserID,
		Intent:         intent,
		Urgency:        urgency,
		Score:          urgency,
		DueAt:          record.Timestamp.Add(delay),
		// 过期窗口放宽到分钟级：真人不会因为隔了 11 秒就不回，
		// 10s 的旧值会让 worker 忙时错过的消息永久静默。
		ExpiresAt:      record.Timestamp.Add(2 * time.Minute),
		Uncertainty:    1 - urgency,
		ReasonCode:     reason,
		DeliveryTarget: "group",
		Status:         humandomain.CandidatePending,
	}
}

// jitteredDelay 在 [lo, hi] 内取均匀随机延迟，让回复节奏有真人式的方差。
func jitteredDelay(lo, hi time.Duration) time.Duration {
	if hi <= lo {
		return lo
	}
	return lo + time.Duration(rand.Int64N(int64(hi-lo)))
}

// memberJoinCandidate 为入群等群事件生成低优先级招呼时机：想不想欢迎
// 由模型抉择（stay_silent 即安静地无视），所以分数压在阈值边缘。
func memberJoinCandidate(record humandomain.EventRecord) humandomain.ThoughtCandidate {
	return humandomain.ThoughtCandidate{
		CandidateID:    record.EventID + "-candidate",
		SourceEventIDs: []string{record.EventID},
		TopicID:        "member_join",
		Addressee:      record.UserID,
		Intent:         "acknowledge",
		Urgency:        0.55,
		Score:          0.55,
		DueAt:          record.Timestamp.Add(jitteredDelay(2*time.Second, 8*time.Second)),
		ExpiresAt:      record.Timestamp.Add(time.Minute),
		Uncertainty:    0.45,
		ReasonCode:     "member_joined",
		DeliveryTarget: "group",
		Status:         humandomain.CandidatePending,
	}
}

// pokeCandidate 为被戳事件生成高优先级回应机会。延迟略长于被 @，
// 留出「愣了一下才反应过来」的自然间隔。
func pokeCandidate(record humandomain.EventRecord) humandomain.ThoughtCandidate {
	return humandomain.ThoughtCandidate{
		CandidateID:    record.EventID + "-candidate",
		SourceEventIDs: []string{record.EventID},
		TopicID:        "poke",
		Addressee:      record.UserID,
		Intent:         "poke_reply",
		Urgency:        0.8,
		Score:          0.8,
		DueAt:          record.Timestamp.Add(jitteredDelay(1200*time.Millisecond, 3*time.Second)),
		ExpiresAt:      record.Timestamp.Add(time.Minute),
		Uncertainty:    0.2,
		ReasonCode:     "poked",
		DeliveryTarget: "group",
		Status:         humandomain.CandidatePending,
	}
}

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

func enqueueCandidate(memory *humandomain.GroupWorkingMemory, candidate humandomain.ThoughtCandidate) bool {
	for _, existing := range memory.Candidates {
		if existing.CandidateID == candidate.CandidateID {
			return false
		}
	}
	memory.Candidates = append(memory.Candidates, candidate)
	memory.Version++
	memory.LastUpdatedAt = time.Now()
	return true
}

func enrichMedia(memory *humandomain.GroupWorkingMemory, eventID string, descriptors []mediadomain.MediaDescriptor) {
	if eventID == "" || !eventInTail(memory.RecentTail, eventID) {
		return
	}
	if memory.MediaByEvent == nil {
		memory.MediaByEvent = make(map[string][]mediadomain.MediaDescriptor)
	}
	memory.MediaByEvent[eventID] = append([]mediadomain.MediaDescriptor(nil), descriptors...)
}

func pruneMedia(memory *humandomain.GroupWorkingMemory) {
	if len(memory.MediaByEvent) == 0 {
		return
	}
	for eventID := range memory.MediaByEvent {
		if !eventInTail(memory.RecentTail, eventID) {
			delete(memory.MediaByEvent, eventID)
		}
	}
}

func eventInTail(records []humandomain.EventRecord, eventID string) bool {
	for _, record := range records {
		if record.EventID == eventID {
			return true
		}
	}
	return false
}

func memoryTopic(text string) string {
	text = strings.TrimSpace(text)
	if len([]rune(text)) > 48 {
		return string([]rune(text)[:48])
	}
	return text
}

func appendUnique(items []string, value string) []string {
	if slices.Contains(items, value) {
		return items
	}
	return append(items, value)
}

func claimCandidate(memory *humandomain.GroupWorkingMemory, now time.Time, minScore float64) *humandomain.ThoughtCandidate {
	var selected *humandomain.ThoughtCandidate
	for i := range memory.Candidates {
		candidate := &memory.Candidates[i]
		if candidate.Status != humandomain.CandidatePending && candidate.Status != humandomain.CandidateDeferred {
			continue
		}
		if candidate.Score < minScore || now.Before(candidate.DueAt) {
			continue
		}
		if !now.Before(candidate.ExpiresAt) {
			candidate.Status = humandomain.CandidateExpired
			continue
		}
		if selected == nil || candidate.Urgency > selected.Urgency || (candidate.Urgency == selected.Urgency && candidate.DueAt.Before(selected.DueAt)) {
			selected = candidate
		}
	}
	if selected == nil {
		return nil
	}
	selected.Status = humandomain.CandidateAccepted
	copy := *selected
	copy.SourceEventIDs = append([]string(nil), selected.SourceEventIDs...)
	return &copy
}

// pruneCandidates bounds long-lived group state. Terminal records are removed
// first; if live work itself exceeds the limit, the newest candidates win so a
// burst does not leave only stale work at the head of the queue.
func pruneCandidates(memory *humandomain.GroupWorkingMemory, max int) {
	if max <= 0 || len(memory.Candidates) <= max {
		return
	}
	liveCount := 0
	for _, candidate := range memory.Candidates {
		if isLive(candidate.Status) {
			liveCount++
		}
	}
	keepIndex := make(map[int]struct{}, max)
	startLive := 0
	if liveCount > max {
		startLive = liveCount - max
	}
	seenLive := 0
	for i, candidate := range memory.Candidates {
		if !isLive(candidate.Status) {
			continue
		}
		if seenLive >= startLive {
			keepIndex[i] = struct{}{}
		}
		seenLive++
	}
	for i := len(memory.Candidates) - 1; i >= 0 && len(keepIndex) < max; i-- {
		if !isLive(memory.Candidates[i].Status) {
			keepIndex[i] = struct{}{}
		}
	}
	keep := make([]humandomain.ThoughtCandidate, 0, len(keepIndex))
	for i, candidate := range memory.Candidates {
		if _, ok := keepIndex[i]; ok {
			keep = append(keep, candidate)
		}
	}
	memory.Candidates = keep
}

func completeCandidate(memory *humandomain.GroupWorkingMemory, candidateID string) {
	for i := range memory.Candidates {
		if memory.Candidates[i].CandidateID == candidateID && memory.Candidates[i].Status == humandomain.CandidateAccepted {
			memory.Candidates[i].Status = humandomain.CandidateCompleted
			return
		}
	}
}

func canExecuteCandidate(memory *humandomain.GroupWorkingMemory, candidateID string, now time.Time) bool {
	for i := range memory.Candidates {
		candidate := &memory.Candidates[i]
		if candidate.CandidateID != candidateID {
			continue
		}
		if candidate.Status != humandomain.CandidateAccepted {
			return false
		}
		if !now.Before(candidate.ExpiresAt) {
			candidate.Status = humandomain.CandidateExpired
			return false
		}
		return true
	}
	return false
}

func cloneMemory(memory humandomain.GroupWorkingMemory) humandomain.GroupWorkingMemory {
	memory.RecentTail = append([]humandomain.EventRecord(nil), memory.RecentTail...)
	memory.OpenLoops = append([]string(nil), memory.OpenLoops...)
	memory.CurrentBurst.EventIDs = append([]string(nil), memory.CurrentBurst.EventIDs...)
	memory.Candidates = append([]humandomain.ThoughtCandidate(nil), memory.Candidates...)
	for i := range memory.Candidates {
		memory.Candidates[i].SourceEventIDs = append([]string(nil), memory.Candidates[i].SourceEventIDs...)
	}
	if len(memory.MediaByEvent) > 0 {
		media := make(map[string][]mediadomain.MediaDescriptor, len(memory.MediaByEvent))
		for eventID, descriptors := range memory.MediaByEvent {
			media[eventID] = append([]mediadomain.MediaDescriptor(nil), descriptors...)
		}
		memory.MediaByEvent = media
	}
	return memory
}
