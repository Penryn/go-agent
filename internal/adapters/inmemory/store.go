package inmemory

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	profiledomain "github.com/phlin/go-agent/internal/domain/profile"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

var (
	_ ports.MemoryStore       = (*Store)(nil)
	_ ports.MemeStore         = (*Store)(nil)
	_ ports.ProfileStore      = (*Store)(nil)
	_ ports.RuntimeStateStore = (*Store)(nil)
	_ ports.OutboundSender    = (*Sender)(nil)
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
	}
}

func (s *Store) ArchiveEvent(_ context.Context, event conversationdomain.ConversationEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.eventsByGroup[event.GroupID] = append(s.eventsByGroup[event.GroupID], event)
	return nil
}

func (s *Store) RecentEvents(_ context.Context, groupID int64, limit int) ([]conversationdomain.ConversationEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events := s.eventsByGroup[groupID]
	if limit <= 0 || len(events) <= limit {
		return append([]conversationdomain.ConversationEvent(nil), events...), nil
	}

	start := len(events) - limit
	return append([]conversationdomain.ConversationEvent(nil), events[start:]...), nil
}

func (s *Store) UpsertMemory(_ context.Context, record memorydomain.MemoryRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

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

	result := make([]memorydomain.MemoryRecord, 0, len(s.memories))
	needle := strings.ToLower(strings.TrimSpace(query.Query))
	for _, record := range s.memories {
		if query.Scope != "" && record.Scope != query.Scope {
			continue
		}
		if len(query.Types) > 0 && !contains(query.Types, record.Type) {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(record.Content), needle) && !strings.Contains(strings.ToLower(record.Subject), needle) {
			continue
		}
		result = append(result, record)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Importance == result[j].Importance {
			return result[i].CreatedAt.After(result[j].CreatedAt)
		}
		return result[i].Importance > result[j].Importance
	})

	if query.TopK > 0 && len(result) > query.TopK {
		result = result[:query.TopK]
	}

	return result, nil
}

func (s *Store) UpsertMeme(_ context.Context, asset mediadomain.MemeAsset, descriptor mediadomain.MemeDescriptor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memeAssets[asset.MemeID] = asset
	s.memeDesc[descriptor.MemeID] = descriptor
	return nil
}

func (s *Store) SearchMemes(_ context.Context, query ports.MemeQuery) ([]mediadomain.MemeSearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	needle := strings.ToLower(strings.TrimSpace(query.Query))
	result := make([]mediadomain.MemeSearchResult, 0, len(s.memeDesc))
	for memeID, descriptor := range s.memeDesc {
		if query.GroupID != 0 {
			asset, ok := s.memeAssets[memeID]
			if !ok || (asset.GroupID != query.GroupID && asset.GroupID != 0) {
				continue
			}
		}

		score := 0.0
		terms := []string{}
		if needle == "" {
			score = 0.5
		}
		if strings.Contains(strings.ToLower(descriptor.Summary), needle) {
			score += 0.6
			terms = append(terms, descriptor.Summary)
		}
		for _, keyword := range descriptor.Keywords {
			if strings.Contains(strings.ToLower(keyword), needle) {
				score += 0.4
				terms = append(terms, keyword)
			}
		}
		if score == 0 && needle != "" {
			continue
		}
		result = append(result, mediadomain.MemeSearchResult{
			MemeID:       memeID,
			Score:        score,
			MatchType:    "keyword",
			MatchedTerms: terms,
			Descriptor:   descriptor,
		})
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Score > result[j].Score })
	if query.TopK > 0 && len(result) > query.TopK {
		result = result[:query.TopK]
	}
	return result, nil
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

	descriptor, ok := s.memeDesc[memeID]
	if !ok {
		return errors.New("meme not found")
	}
	descriptor.UpdatedAt = time.Now()
	s.memeDesc[memeID] = descriptor
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
		return state, nil
	}

	now := time.Now()
	return personadomain.PersonaState{
		PersonaID: personaID,
		GroupID:   groupID,
		Mood:      "steady",
		Energy:    "normal",
		UpdatedAt: now,
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

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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
