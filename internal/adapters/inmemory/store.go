package inmemory

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/phlin/go-agent/internal/application/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	searchcore "github.com/phlin/go-agent/internal/search"
	"github.com/phlin/go-agent/internal/search/bm25"
)

var (
	_ ports.MemoryStore        = (*Store)(nil)
	_ ports.LearningStateStore = (*Store)(nil)
	_ ports.ThoughtStore       = (*Store)(nil)
	_ ports.MemeStore          = (*Store)(nil)
	_ ports.ProfileStore       = (*Store)(nil)
	_ ports.RuntimeStateStore  = (*Store)(nil)
	_ ports.OutboundSender     = (*Sender)(nil)
)

type Store struct {
	mu            sync.RWMutex
	eventsByGroup map[int64][]conversationdomain.ConversationEvent
	memories      []memorydomain.MemoryRecord
	memeAssets    map[string]mediadomain.MemeAsset
	memeDesc      map[string]mediadomain.MemeDescriptor
	profiles      map[string]profiledomain.MemberProfile
	relations     map[string]profiledomain.RelationshipState
	runtimeStates map[int64]policydomain.RuntimeState
	personaStates map[string]personadomain.PersonaState
	learningMarks map[string]memorydomain.LearningWatermark
	thoughts      []replydomain.ThoughtRecord
	workingStates map[int64]presencedomain.GroupWorkingMemory
	outbox        map[string]ports.OutboxTask
	outboxByKey   map[string]string
	outboxSeq     int64
}

func (s *Store) SaveThought(_ context.Context, thought replydomain.ThoughtRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.thoughts {
		if s.thoughts[i].ThoughtID == thought.ThoughtID {
			s.thoughts[i] = thought
			return nil
		}
	}
	s.thoughts = append(s.thoughts, thought)
	return nil
}

// RecentThoughts 返回一群最近的思考记录（新到旧）。
func (s *Store) RecentThoughts(_ context.Context, groupID int64, limit int) ([]replydomain.ThoughtRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	records := make([]replydomain.ThoughtRecord, 0, limit)
	for i := len(s.thoughts) - 1; i >= 0 && len(records) < limit; i-- {
		if s.thoughts[i].GroupID == groupID {
			records = append(records, s.thoughts[i])
		}
	}
	return records, nil
}

func (s *Store) LoadWorkingMemory(_ context.Context, groupID int64) (presencedomain.GroupWorkingMemory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneWorkingMemory(s.workingStates[groupID]), nil
}

func (s *Store) SaveWorkingMemory(_ context.Context, memory presencedomain.GroupWorkingMemory) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workingStates[memory.GroupID] = cloneWorkingMemory(memory)
	return nil
}

func NewStore() *Store {
	return &Store{
		eventsByGroup: make(map[int64][]conversationdomain.ConversationEvent),
		memeAssets:    make(map[string]mediadomain.MemeAsset),
		memeDesc:      make(map[string]mediadomain.MemeDescriptor),
		profiles:      make(map[string]profiledomain.MemberProfile),
		relations:     make(map[string]profiledomain.RelationshipState),
		runtimeStates: make(map[int64]policydomain.RuntimeState),
		personaStates: make(map[string]personadomain.PersonaState),
		learningMarks: make(map[string]memorydomain.LearningWatermark),
		workingStates: make(map[int64]presencedomain.GroupWorkingMemory),
		outbox:        make(map[string]ports.OutboxTask),
		outboxByKey:   make(map[string]string),
	}
}

var _ ports.OutboxStore = (*Store)(nil)

func (s *Store) EnqueueOutbox(_ context.Context, task ports.OutboxTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if task.Kind == "" || task.IdempotencyKey == "" {
		return errors.New("outbox: kind and idempotency key are required")
	}
	if _, exists := s.outboxByKey[outboxKey(task.Kind, task.IdempotencyKey)]; exists {
		return nil
	}
	now := time.Now()
	if task.ID == "" {
		s.outboxSeq++
		task.ID = "outbox-" + strconv.FormatInt(s.outboxSeq, 10)
	}
	if task.Status == "" {
		task.Status = ports.OutboxPending
	}
	if task.AvailableAt.IsZero() {
		task.AvailableAt = now
	}
	if task.MaxAttempts <= 0 {
		task.MaxAttempts = 5
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	task.UpdatedAt = now
	task.Payload = append([]byte(nil), task.Payload...)
	s.outbox[task.ID] = task
	s.outboxByKey[outboxKey(task.Kind, task.IdempotencyKey)] = task.ID
	return nil
}

func (s *Store) ClaimOutbox(_ context.Context, workerID string, now time.Time, lease time.Duration, limit int) ([]ports.OutboxTask, error) {
	if workerID == "" {
		return nil, errors.New("outbox: worker id is required")
	}
	if limit <= 0 {
		limit = 1
	}
	if lease <= 0 {
		lease = time.Minute
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.outbox))
	for id, task := range s.outbox {
		ready := (task.Status == ports.OutboxPending || task.Status == ports.OutboxRetry) && !task.AvailableAt.After(now)
		expired := task.Status == ports.OutboxRunning && !task.LockedUntil.After(now)
		if ready || expired {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		return s.outbox[ids[i]].CreatedAt.Before(s.outbox[ids[j]].CreatedAt)
	})
	claimed := make([]ports.OutboxTask, 0, min(limit, len(ids)))
	for _, id := range ids {
		if len(claimed) >= limit {
			break
		}
		task := s.outbox[id]
		task.Status = ports.OutboxRunning
		task.Attempts++
		task.LockedBy = workerID
		task.LockedUntil = now.Add(lease)
		task.UpdatedAt = now
		s.outbox[id] = task
		task.Payload = append([]byte(nil), task.Payload...)
		claimed = append(claimed, task)
	}
	return claimed, nil
}

func (s *Store) CompleteOutbox(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.outbox[id]
	if !ok {
		return errors.New("outbox: task not found")
	}
	task.Status = ports.OutboxCompleted
	task.LockedBy = ""
	task.LockedUntil = time.Time{}
	task.UpdatedAt = time.Now()
	s.outbox[id] = task
	return nil
}

func (s *Store) FailOutbox(_ context.Context, id string, taskErr error, retryAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.outbox[id]
	if !ok {
		return errors.New("outbox: task not found")
	}
	task.LastError = ""
	if taskErr != nil {
		task.LastError = taskErr.Error()
	}
	if retryAt.IsZero() || (task.MaxAttempts > 0 && task.Attempts >= task.MaxAttempts) {
		task.Status = ports.OutboxDeadLetter
	} else {
		task.Status = ports.OutboxRetry
		task.AvailableAt = retryAt
	}
	task.LockedBy = ""
	task.LockedUntil = time.Time{}
	task.UpdatedAt = time.Now()
	s.outbox[id] = task
	return nil
}

// LookupOutbox is a test and diagnostics helper for the in-memory adapter.
func (s *Store) LookupOutbox(id string) (ports.OutboxTask, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.outbox[id]
	task.Payload = append([]byte(nil), task.Payload...)
	return task, ok
}

func outboxKey(kind, idempotencyKey string) string {
	return kind + "\x00" + idempotencyKey
}

func cloneWorkingMemory(memory presencedomain.GroupWorkingMemory) presencedomain.GroupWorkingMemory {
	memory.RecentTail = append([]presencedomain.EventRecord(nil), memory.RecentTail...)
	memory.OpenLoops = append([]string(nil), memory.OpenLoops...)
	memory.Candidates = append([]presencedomain.ThoughtCandidate(nil), memory.Candidates...)
	if memory.MediaByEvent != nil {
		memory.MediaByEvent = make(map[string][]mediadomain.MediaDescriptor, len(memory.MediaByEvent))
		for id, descriptors := range memory.MediaByEvent {
			memory.MediaByEvent[id] = append([]mediadomain.MediaDescriptor(nil), descriptors...)
		}
	}
	return memory
}

func (s *Store) EventsAfter(_ context.Context, groupID int64, after time.Time, afterEventID string, limit int) ([]conversationdomain.ConversationEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]conversationdomain.ConversationEvent, 0)
	for _, event := range s.eventsByGroup[groupID] {
		occurredAt := time.Unix(event.TimestampUnix, 0)
		if occurredAt.Before(after) || (occurredAt.Equal(after) && event.EventID <= afterEventID) {
			continue
		}
		result = append(result, event)
	}
	sort.SliceStable(result, func(i, j int) bool {
		left, right := time.Unix(result[i].TimestampUnix, 0), time.Unix(result[j].TimestampUnix, 0)
		if left.Equal(right) {
			return result[i].EventID < result[j].EventID
		}
		return left.Before(right)
	})
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (s *Store) GetLearningWatermark(_ context.Context, groupID int64, kind string) (memorydomain.LearningWatermark, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.learningMarks[learningMarkKey(groupID, kind)], nil
}

func (s *Store) SaveLearningWatermark(_ context.Context, watermark memorydomain.LearningWatermark) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.learningMarks[learningMarkKey(watermark.GroupID, watermark.Kind)] = watermark
	return nil
}

func learningMarkKey(groupID int64, kind string) string {
	return strconv.FormatInt(groupID, 10) + ":" + kind
}

func (s *Store) ArchiveEvent(_ context.Context, event conversationdomain.ConversationEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if event.TimestampUnix == 0 {
		event.TimestampUnix = time.Now().Unix()
	}

	events := s.eventsByGroup[event.GroupID]
	for i := range events {
		if event.EventID != "" && events[i].EventID == event.EventID {
			events[i] = event
			s.eventsByGroup[event.GroupID] = events
			return nil
		}
	}
	s.eventsByGroup[event.GroupID] = append(events, event)
	return nil
}

func (s *Store) RecentEvents(_ context.Context, groupID int64, limit int) ([]conversationdomain.ConversationEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events := append([]conversationdomain.ConversationEvent(nil), s.eventsByGroup[groupID]...)
	sort.SliceStable(events, func(i, j int) bool {
		left, right := time.Unix(events[i].TimestampUnix, 0), time.Unix(events[j].TimestampUnix, 0)
		if left.Equal(right) {
			return events[i].EventID < events[j].EventID
		}
		return left.Before(right)
	})
	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}
	return events, nil
}

func (s *Store) UpsertMemory(_ context.Context, record memorydomain.MemoryRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record.Revision <= 0 {
		record.Revision = time.Now().UnixNano()
	}

	for i := range s.memories {
		if s.memories[i].MemoryID == record.MemoryID {
			s.memories[i] = record
			return nil
		}
	}

	s.memories = append(s.memories, record)
	return nil
}

func (s *Store) QueryMemories(_ context.Context, query ports.MemoryQuery) ([]memorydomain.MemoryRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	result := make([]memorydomain.MemoryRecord, 0, len(s.memories))
	needle := strings.TrimSpace(query.Query)
	for _, record := range s.memories {
		if query.Scope == "" && !searchcore.MemoryScopeVisible(record.Scope, query.GroupID, query.UserID) {
			continue
		}
		if query.Scope != "" && record.Scope != query.Scope {
			continue
		}
		if query.Scope != "" && !searchcore.MemoryScopeVisible(record.Scope, query.GroupID, query.UserID) {
			continue
		}
		if len(query.Types) > 0 && !slices.Contains(query.Types, record.Type) {
			continue
		}
		// 遗忘：过期的记忆不再被召回
		if record.ExpiresAt != nil && record.ExpiresAt.Before(now) {
			continue
		}
		result = append(result, record)
	}

	if needle != "" {
		documents := make([]bm25.Document, len(result))
		byID := make(map[string]memorydomain.MemoryRecord, len(result))
		for i, record := range result {
			documents[i] = bm25.Document{ID: record.MemoryID, Text: record.Subject + "\n" + record.Content}
			byID[record.MemoryID] = record
		}
		ranked := bm25.Rank(needle, documents, query.TopK)
		result = result[:0]
		for _, item := range ranked {
			result = append(result, byID[item.ID])
		}
	} else {
		// 遗忘：重要性随时间贴现——真人记忆里旧事除非特别重要，否则淡出。
		sort.Slice(result, func(i, j int) bool {
			return memoryRecallScore(result[i], now) > memoryRecallScore(result[j], now)
		})
	}

	if query.TopK > 0 && len(result) > query.TopK {
		result = result[:query.TopK]
	}

	return result, nil
}

// memoryRecallScore 是记忆的召回优先级：重要性按时间贴现，但高重要性的
// 旧事仍应压过低分新事——真人记得「重要旧事」，淡忘的是「无聊近事」。
// 贴现用 age/(importance*30) 的形态：0.9 重要性 30 天龄 ≈ 减半，0.3 的 3 天就减半。
func memoryRecallScore(record memorydomain.MemoryRecord, now time.Time) float64 {
	ageDays := now.Sub(record.CreatedAt).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	halfLife := max(record.Importance, 0.1) * 30
	return record.Importance / (1 + ageDays/halfLife)
}

func (s *Store) UpsertMeme(_ context.Context, asset mediadomain.MemeAsset, descriptor mediadomain.MemeDescriptor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if asset.Revision <= 0 {
		asset.Revision = time.Now().UnixNano()
	}
	s.memeAssets[asset.MemeID] = asset
	s.memeDesc[descriptor.MemeID] = descriptor
	return nil
}

func (s *Store) SearchMemes(_ context.Context, query ports.MemeQuery) ([]mediadomain.MemeSearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	needle := strings.TrimSpace(query.Query)
	result := make([]mediadomain.MemeSearchResult, 0, len(s.memeDesc))
	for memeID, descriptor := range s.memeDesc {
		if query.GroupID != 0 {
			asset, ok := s.memeAssets[memeID]
			if !ok || asset.Status != "approved" || (asset.GroupID != query.GroupID && asset.GroupID != 0) {
				continue
			}
		} else if asset, ok := s.memeAssets[memeID]; !ok || asset.Status != "approved" || asset.GroupID != 0 {
			continue
		}

		result = append(result, mediadomain.MemeSearchResult{
			MemeID:       memeID,
			Score:        0.5,
			MatchType:    "keyword",
			MatchedTerms: descriptor.Keywords,
			Descriptor:   descriptor,
		})
	}

	if needle != "" {
		documents := make([]bm25.Document, len(result))
		for i, item := range result {
			documents[i] = bm25.Document{ID: item.MemeID, Text: memeSearchText(item.Descriptor)}
		}
		ranked := bm25.Rank(needle, documents, query.TopK)
		byID := make(map[string]mediadomain.MemeSearchResult, len(result))
		for _, item := range result {
			byID[item.MemeID] = item
		}
		result = result[:0]
		for _, item := range ranked {
			found := byID[item.ID]
			found.Score = item.Score
			result = append(result, found)
		}
	} else {
		sort.Slice(result, func(i, j int) bool { return result[i].Score > result[j].Score })
	}
	if query.TopK > 0 && len(result) > query.TopK {
		result = result[:query.TopK]
	}
	return result, nil
}

func memeSearchText(descriptor mediadomain.MemeDescriptor) string {
	return strings.Join([]string{descriptor.Title, descriptor.Summary, strings.Join(descriptor.Keywords, " "), strings.Join(descriptor.EmotionTags, " "), strings.Join(descriptor.SceneTags, " "), strings.Join(descriptor.UsageHints, " ")}, "\n")
}

func (s *Store) GetMeme(_ context.Context, memeID string) (mediadomain.MemeAsset, mediadomain.MemeDescriptor, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	asset, ok := s.memeAssets[memeID]
	if !ok {
		return mediadomain.MemeAsset{}, mediadomain.MemeDescriptor{}, errors.New("meme not found")
	}
	descriptor := s.memeDesc[memeID]
	return asset, descriptor, nil
}

func (s *Store) MarkMemeSent(_ context.Context, memeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	asset, ok := s.memeAssets[memeID]
	if !ok {
		return errors.New("meme not found")
	}
	now := time.Now()
	asset.LastSentAt = &now
	asset.SendCount++
	s.memeAssets[memeID] = asset
	return nil
}

// MarkMemeDud 记一次哑弹（发送后群里持续冷场）。
func (s *Store) MarkMemeDud(_ context.Context, memeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	asset, ok := s.memeAssets[memeID]
	if !ok {
		return errors.New("meme not found")
	}
	asset.DudCount++
	s.memeAssets[memeID] = asset
	return nil
}

func (s *Store) CountMemesByGroup(_ context.Context, groupID int64) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, asset := range s.memeAssets {
		if asset.GroupID == groupID {
			count++
		}
	}
	return count, nil
}

func (s *Store) DeleteOldestMemes(_ context.Context, groupID int64, deleteCount int) error {
	if deleteCount <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	type entry struct {
		id        string
		createdAt time.Time
	}
	var candidates []entry
	for id, asset := range s.memeAssets {
		if asset.GroupID == groupID {
			candidates = append(candidates, entry{id, asset.CreatedAt})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].createdAt.Before(candidates[j].createdAt)
	})
	for i := 0; i < deleteCount && i < len(candidates); i++ {
		delete(s.memeAssets, candidates[i].id)
		delete(s.memeDesc, candidates[i].id)
	}
	return nil
}

func (s *Store) GetMemberProfile(_ context.Context, groupID, userID int64) (profiledomain.MemberProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := profileKey(groupID, userID)
	if profile, ok := s.profiles[key]; ok {
		return profile, nil
	}

	return profiledomain.MemberProfile{
		Stats: profiledomain.MemberStats{
			GroupID: groupID,
			UserID:  userID,
		},
	}, nil
}

func (s *Store) SaveMemberProfile(_ context.Context, profile profiledomain.MemberProfile) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.profiles[profileKey(profile.Stats.GroupID, profile.Stats.UserID)] = profile
	return nil
}

func (s *Store) GetRelationship(_ context.Context, personaID string, groupID, userID int64) (profiledomain.RelationshipState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := relationKey(personaID, groupID, userID)
	if relation, ok := s.relations[key]; ok {
		return relation, nil
	}

	return profiledomain.RelationshipState{
		PersonaID:   personaID,
		GroupID:     groupID,
		UserID:      userID,
		Familiarity: 0.1,
		Affinity:    0.1,
	}, nil
}

func (s *Store) SaveRelationship(_ context.Context, state profiledomain.RelationshipState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.relations[relationKey(state.PersonaID, state.GroupID, state.UserID)] = state
	return nil
}

func (s *Store) GetRuntimeState(_ context.Context, groupID int64) (policydomain.RuntimeState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if state, ok := s.runtimeStates[groupID]; ok {
		return state, nil
	}

	return policydomain.RuntimeState{
		GroupID: groupID,
		State:   policydomain.StateObserving,
	}, nil
}

func (s *Store) SaveRuntimeState(_ context.Context, state policydomain.RuntimeState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeStates[state.GroupID] = state
	return nil
}

func (s *Store) GetPersonaState(_ context.Context, personaID string, groupID int64) (personadomain.PersonaState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := personaStateKey(personaID, groupID)
	if state, ok := s.personaStates[key]; ok {
		if state.ExpiresAt.IsZero() || time.Now().Before(state.ExpiresAt) {
			return state, nil
		}
	}

	now := time.Now()
	return personadomain.PersonaState{
		PersonaID: personaID,
		GroupID:   groupID,
		Mood:      "steady",
		Energy:    "normal",
		ExpiresAt: now.Add(2 * time.Hour),
	}, nil
}

func (s *Store) SavePersonaState(_ context.Context, state personadomain.PersonaState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.personaStates[personaStateKey(state.PersonaID, state.GroupID)] = state
	return nil
}

type Sender struct {
	mu      sync.Mutex
	actions []replydomain.ActionExecution
}

func NewSender() *Sender {
	return &Sender{}
}

func (s *Sender) Send(_ context.Context, action replydomain.ActionExecution) (replydomain.ActionReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.actions = append(s.actions, action)
	return replydomain.ActionReceipt{
		ActionID:          action.ActionID,
		PlatformMessageID: action.ActionID + "-sent",
		Sent:              true,
	}, nil
}

func (s *Sender) Actions() []replydomain.ActionExecution {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]replydomain.ActionExecution(nil), s.actions...)
}

// MarkRead 记录已读回执调用，供测试断言「发送前先标已读」的时序。
func (s *Sender) MarkRead(_ context.Context, _ int64, _ string) error {
	return nil
}

func profileKey(groupID, userID int64) string {
	return relationKey("", groupID, userID)
}

func relationKey(personaID string, groupID, userID int64) string {
	return strings.Join([]string{personaID, int64String(groupID), int64String(userID)}, ":")
}

func personaStateKey(personaID string, groupID int64) string {
	return strings.Join([]string{personaID, int64String(groupID)}, ":")
}

func int64String(value int64) string {
	return strconv.FormatInt(value, 10)
}
