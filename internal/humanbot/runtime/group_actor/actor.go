package group_actor

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
	"github.com/phlin/go-agent/internal/humanbot/runtime/ingress"
)

const defaultTailSize = 32
const burstWindow = 2 * time.Second

type Manager struct {
	log      ingress.EventLog
	archive  ports.MemoryStore
	tailSize int

	mu     sync.Mutex
	closed bool
	groups map[int64]*actor
}

type Option func(*Manager)

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

func NewManager(log ingress.EventLog, opts ...Option) *Manager {
	m := &Manager{log: log, tailSize: defaultTailSize, groups: make(map[int64]*actor)}
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
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return humandomain.GroupWorkingMemory{}, errors.New("group actor: manager is closed")
	}
	a := m.groups[record.GroupID]
	if a == nil {
		a = newActor(record.GroupID, m.tailSize)
		m.groups[record.GroupID] = a
	}
	m.mu.Unlock()
	return a.observe(ctx, record)
}

func (m *Manager) Snapshot(ctx context.Context, groupID int64) (humandomain.GroupWorkingMemory, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return humandomain.GroupWorkingMemory{}, errors.New("group actor: manager is closed")
	}
	a := m.groups[groupID]
	if a == nil {
		a = newActor(groupID, m.tailSize)
		m.groups[groupID] = a
	}
	m.mu.Unlock()
	return a.snapshot(ctx)
}

func (m *Manager) ClaimDue(ctx context.Context, groupID int64, now time.Time, minScore float64) (humandomain.ThoughtCandidate, bool, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return humandomain.ThoughtCandidate{}, false, errors.New("group actor: manager is closed")
	}
	a := m.groups[groupID]
	m.mu.Unlock()
	if a == nil {
		return humandomain.ThoughtCandidate{}, false, nil
	}
	resultCh := make(chan result, 1)
	select {
	case a.mailbox <- request{op: "claim", now: now, minScore: minScore, result: resultCh}:
	case <-ctx.Done():
		return humandomain.ThoughtCandidate{}, false, ctx.Err()
	case <-a.done:
		return humandomain.ThoughtCandidate{}, false, errors.New("group actor: actor stopped")
	}
	select {
	case result := <-resultCh:
		if result.candidate == nil {
			return humandomain.ThoughtCandidate{}, false, nil
		}
		return *result.candidate, true, result.err
	case <-ctx.Done():
		return humandomain.ThoughtCandidate{}, false, ctx.Err()
	case <-a.done:
		return humandomain.ThoughtCandidate{}, false, errors.New("group actor: actor stopped")
	}
}

func (m *Manager) Complete(ctx context.Context, groupID int64, candidateID string) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("group actor: manager is closed")
	}
	a := m.groups[groupID]
	m.mu.Unlock()
	if a == nil {
		return nil
	}
	resultCh := make(chan result, 1)
	select {
	case a.mailbox <- request{op: "complete", candidateID: candidateID, result: resultCh}:
	case <-ctx.Done():
		return ctx.Err()
	case <-a.done:
		return errors.New("group actor: actor stopped")
	}
	select {
	case result := <-resultCh:
		return result.err
	case <-ctx.Done():
		return ctx.Err()
	case <-a.done:
		return errors.New("group actor: actor stopped")
	}
}

// CanExecute confirms that an accepted candidate has not been superseded or
// expired while a worker was waiting on group serialization or model latency.
func (m *Manager) CanExecute(ctx context.Context, groupID int64, candidateID string, now time.Time) (bool, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return false, errors.New("group actor: manager is closed")
	}
	a := m.groups[groupID]
	m.mu.Unlock()
	if a == nil {
		return false, nil
	}
	return a.canExecute(ctx, candidateID, now)
}

// EnrichMedia writes asynchronous perception results through the owning group
// actor. Workers never mutate working memory directly.
func (m *Manager) EnrichMedia(ctx context.Context, groupID int64, eventID string, descriptors []mediadomain.MediaDescriptor) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("group actor: manager is closed")
	}
	a := m.groups[groupID]
	m.mu.Unlock()
	if a == nil {
		return nil
	}
	return a.enrichMedia(ctx, eventID, descriptors)
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

func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	groups := make([]*actor, 0, len(m.groups))
	for _, a := range m.groups {
		groups = append(groups, a)
	}
	m.mu.Unlock()
	for _, a := range groups {
		a.close()
	}
	if closer, ok := m.log.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

type request struct {
	op          string
	record      humandomain.EventRecord
	now         time.Time
	minScore    float64
	candidateID string
	eventID     string
	descriptors []mediadomain.MediaDescriptor
	result      chan result
}

type result struct {
	memory    humandomain.GroupWorkingMemory
	candidate *humandomain.ThoughtCandidate
	valid     bool
	err       error
}

type actor struct {
	groupID  int64
	tailSize int
	mailbox  chan request
	stop     chan struct{}
	done     chan struct{}
	seen     map[string]struct{}
}

func newActor(groupID int64, tailSize int) *actor {
	a := &actor{
		groupID:  groupID,
		tailSize: tailSize,
		mailbox:  make(chan request, 128),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		seen:     make(map[string]struct{}),
	}
	go a.run()
	return a
}

func (a *actor) run() {
	defer close(a.done)
	memory := humandomain.GroupWorkingMemory{GroupID: a.groupID}
	for {
		select {
		case <-a.stop:
			return
		case req := <-a.mailbox:
			switch req.op {
			case "observe":
				if _, duplicate := a.seen[req.record.EventID]; !duplicate {
					a.seen[req.record.EventID] = struct{}{}
					memory = reduce(memory, req.record, a.tailSize)
				}
			case "claim":
				candidate := claimCandidate(&memory, req.now, req.minScore)
				req.result <- result{memory: cloneMemory(memory), candidate: candidate}
				continue
			case "complete":
				completeCandidate(&memory, req.candidateID)
				req.result <- result{memory: cloneMemory(memory)}
				continue
			case "can_execute":
				valid := canExecuteCandidate(&memory, req.candidateID, req.now)
				req.result <- result{memory: cloneMemory(memory), valid: valid}
				continue
			case "enrich_media":
				enrichMedia(&memory, req.eventID, req.descriptors)
			case "snapshot":
			}
			req.result <- result{memory: cloneMemory(memory)}
		}
	}
}

func (a *actor) canExecute(ctx context.Context, candidateID string, now time.Time) (bool, error) {
	resultCh := make(chan result, 1)
	select {
	case a.mailbox <- request{op: "can_execute", candidateID: candidateID, now: now, result: resultCh}:
	case <-ctx.Done():
		return false, ctx.Err()
	case <-a.done:
		return false, errors.New("group actor: actor stopped")
	}
	select {
	case result := <-resultCh:
		return result.valid, result.err
	case <-ctx.Done():
		return false, ctx.Err()
	case <-a.done:
		return false, errors.New("group actor: actor stopped")
	}
}

func (a *actor) enrichMedia(ctx context.Context, eventID string, descriptors []mediadomain.MediaDescriptor) error {
	resultCh := make(chan result, 1)
	copyDescriptors := append([]mediadomain.MediaDescriptor(nil), descriptors...)
	select {
	case a.mailbox <- request{op: "enrich_media", eventID: eventID, descriptors: copyDescriptors, result: resultCh}:
	case <-ctx.Done():
		return ctx.Err()
	case <-a.done:
		return errors.New("group actor: actor stopped")
	}
	select {
	case result := <-resultCh:
		return result.err
	case <-ctx.Done():
		return ctx.Err()
	case <-a.done:
		return errors.New("group actor: actor stopped")
	}
}

func (a *actor) observe(ctx context.Context, record humandomain.EventRecord) (humandomain.GroupWorkingMemory, error) {
	resultCh := make(chan result, 1)
	select {
	case a.mailbox <- request{op: "observe", record: record, result: resultCh}:
	case <-ctx.Done():
		return humandomain.GroupWorkingMemory{}, ctx.Err()
	case <-a.done:
		return humandomain.GroupWorkingMemory{}, errors.New("group actor: actor stopped")
	}
	select {
	case result := <-resultCh:
		return result.memory, result.err
	case <-ctx.Done():
		return humandomain.GroupWorkingMemory{}, ctx.Err()
	case <-a.done:
		return humandomain.GroupWorkingMemory{}, errors.New("group actor: actor stopped")
	}
}

func (a *actor) snapshot(ctx context.Context) (humandomain.GroupWorkingMemory, error) {
	resultCh := make(chan result, 1)
	select {
	case a.mailbox <- request{op: "snapshot", result: resultCh}:
	case <-ctx.Done():
		return humandomain.GroupWorkingMemory{}, ctx.Err()
	case <-a.done:
		return humandomain.GroupWorkingMemory{}, errors.New("group actor: actor stopped")
	}
	select {
	case result := <-resultCh:
		return result.memory, result.err
	case <-ctx.Done():
		return humandomain.GroupWorkingMemory{}, ctx.Err()
	case <-a.done:
		return humandomain.GroupWorkingMemory{}, errors.New("group actor: actor stopped")
	}
}

func (a *actor) close() {
	close(a.stop)
	<-a.done
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
		for _, r := range right {
			if l == r {
				return true
			}
		}
	}
	return false
}

func candidateFor(record humandomain.EventRecord, burst humandomain.ConversationBurst) humandomain.ThoughtCandidate {
	direct := record.Event.MentionedBot || record.Event.NamedBot || record.Event.IsReplyToBot
	intent := "continue_topic"
	urgency := 0.35
	delay := 2 * time.Second
	if direct {
		intent = "answer"
		urgency = 1
		delay = 700 * time.Millisecond
	} else if strings.ContainsAny(record.Event.Text, "?？") {
		intent = "acknowledge"
		urgency = 0.55
	}
	if len(record.Event.Attachments) > 0 && !direct {
		intent = "react"
		urgency = 0.5
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
		ExpiresAt:      record.Timestamp.Add(10 * time.Second),
		Uncertainty:    1 - urgency,
		Status:         humandomain.CandidatePending,
	}
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
	for _, item := range items {
		if item == value {
			return items
		}
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
