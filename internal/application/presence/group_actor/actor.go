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
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
)

const defaultTailSize = 32
const defaultMaxSeen = 2048
const (
	burstWindow    = 700 * time.Millisecond
	burstMaxWindow = 3 * time.Second
)

type Manager struct {
	log      *ingress.MemoryEventLog
	archive  ports.MemoryStore
	state    WorkingMemoryStore
	tailSize int
	maxSeen  int
	idleTTL  time.Duration

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
	return m.observe(ctx, record)
}

// ObserveReplay records an event without starting the online decision loop.
// Synchronous replay owns deliberation and must not produce a second reply.
func (m *Manager) ObserveReplay(ctx context.Context, record presencedomain.EventRecord) (presencedomain.GroupWorkingMemory, error) {
	return m.observe(ctx, record)
}

func (m *Manager) observe(ctx context.Context, record presencedomain.EventRecord) (presencedomain.GroupWorkingMemory, error) {
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
	a.mu.Lock()
	a.touch()
	if _, duplicate := a.seen[record.EventID]; duplicate {
		memory := cloneMemory(a.memory)
		a.mu.Unlock()
		return memory, nil
	}
	next := cloneMemory(a.memory)
	next = reduce(next, record, a.tailSize)
	if err := m.save(ctx, next); err != nil {
		a.mu.Unlock()
		return next, err
	}
	a.memory = next
	a.seen[record.EventID] = struct{}{}
	a.pruneSeen(a.memory.RecentTail)
	memory := cloneMemory(a.memory)
	a.mu.Unlock()
	return memory, nil
}

func (m *Manager) Snapshot(ctx context.Context, groupID int64) (presencedomain.GroupWorkingMemory, error) {
	a, err := m.actor(ctx, groupID)
	if err != nil {
		return presencedomain.GroupWorkingMemory{}, err
	}
	return a.snapshot(), nil
}

// Update applies a small projection mutation under the owning group actor's
// lock and persists it. It is intentionally generic so lifecycle observers
// such as feedback windows do not need a second state store or a second actor.
func (m *Manager) Update(ctx context.Context, groupID int64, update func(*presencedomain.GroupWorkingMemory) error) error {
	if update == nil {
		return errors.New("group actor: update callback is nil")
	}
	a, err := m.actor(ctx, groupID)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.touch()
	next := cloneMemory(a.memory)
	if err := update(&next); err != nil {
		a.mu.Unlock()
		return err
	}
	if err := m.save(ctx, next); err != nil {
		a.mu.Unlock()
		return err
	}
	a.memory = next
	a.mu.Unlock()
	return nil
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
	a.touch()
	next := cloneMemory(a.memory)
	addedEventIDs := make([]string, 0, len(events))
	pendingEventIDs := make(map[string]struct{}, len(events))
	for _, event := range events {
		if event.EventID == "" {
			continue
		}
		if _, seen := a.seen[event.EventID]; seen {
			continue
		}
		if _, pending := pendingEventIDs[event.EventID]; pending {
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
		next = reduce(next, record, a.tailSize)
		addedEventIDs = append(addedEventIDs, event.EventID)
		pendingEventIDs[event.EventID] = struct{}{}
	}
	if err := m.save(ctx, next); err != nil {
		a.mu.Unlock()
		return presencedomain.GroupWorkingMemory{}, err
	}
	a.memory = next
	for _, eventID := range addedEventIDs {
		a.seen[eventID] = struct{}{}
	}
	a.pruneSeen(a.memory.RecentTail)
	memory := cloneMemory(a.memory)
	a.mu.Unlock()
	return memory, nil
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
		m.maxSeen,
		initial,
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

// EnrichMedia writes asynchronous perception results through the owning group
// actor. Workers never mutate working memory directly.
func (m *Manager) EnrichMedia(ctx context.Context, groupID int64, eventID string, descriptors []mediadomain.MediaDescriptor) error {
	a, err := m.actor(ctx, groupID)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.touch()
	next := cloneMemory(a.memory)
	enrichMedia(&next, eventID, descriptors)
	if err := m.save(ctx, next); err != nil {
		return err
	}
	a.memory = next
	return nil
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
	groupID  int64
	tailSize int
	maxSeen  int

	mu           sync.Mutex
	memory       presencedomain.GroupWorkingMemory
	seen         map[string]struct{}
	lastUsedNano atomic.Int64
}

func newActor(groupID int64, tailSize, maxSeen int, initial presencedomain.GroupWorkingMemory) *actor {
	a := &actor{
		groupID:  groupID,
		tailSize: tailSize,
		maxSeen:  maxSeen,
		seen:     make(map[string]struct{}),
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

// snapshot must touch the idle clock: deliberation reads it before thinking,
// and a group mid-deliberation must not be reclaimed by PruneIdle.
func (a *actor) snapshot() presencedomain.GroupWorkingMemory {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.touch()
	return cloneMemory(a.memory)
}

// retireIfIdle reports whether the actor may be dropped. The check happens
// under the lock so a concurrent touch during the wait is respected.
func (a *actor) retireIfIdle(now time.Time, idleTTL time.Duration) (presencedomain.GroupWorkingMemory, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !nowIdle(a.lastUsed(), now, idleTTL) {
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
	memory.FeedbackWindows = append([]presencedomain.FeedbackWindow(nil), memory.FeedbackWindows...)
	for i := range memory.FeedbackWindows {
		memory.FeedbackWindows[i].ObservedEventIDs = append([]string(nil), memory.FeedbackWindows[i].ObservedEventIDs...)
	}
	return memory
}
