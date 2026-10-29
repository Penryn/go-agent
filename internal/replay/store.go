// Package replay keeps all simulated sends and memories local to a replay session.
package replay

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/phlin/go-agent/internal/chat"
	"github.com/phlin/go-agent/internal/conversation"
	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/qq"
)

type projection struct {
	Version int64
	Model   string
	Vector  []float32
}
type Store struct {
	mu             sync.Mutex
	events         []qq.Event
	attempts       map[string]chat.Attempt
	usage          map[string]map[string]bool
	notes          map[string]memory.Note
	vectors        map[string]projection
	cursors        map[int64]int64
	topics         map[int64][]conversation.TopicView
	topicEvents    map[int64][]conversation.TopicEventView
	summaryCursors map[int64]int64
	runs           []memory.LearningRun
	memoryEvents   []string
}

func NewStore() *Store {
	return &Store{attempts: map[string]chat.Attempt{}, usage: map[string]map[string]bool{}, notes: map[string]memory.Note{}, vectors: map[string]projection{}, cursors: map[int64]int64{}, topics: map[int64][]conversation.TopicView{}, topicEvents: map[int64][]conversation.TopicEventView{}, summaryCursors: map[int64]int64{}}
}
func (s *Store) Ingest(_ context.Context, e *qq.Event) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, old := range s.events {
		if old.ID == e.ID {
			return false, nil
		}
		if e.Recalled && old.GroupID == e.GroupID && old.MessageID == e.MessageID {
			s.events[i].Recalled = true
		}
	}
	e.Seq = int64(len(s.events) + 1)
	s.events = append(s.events, *e)
	return true, nil
}
func (s *Store) Recent(_ context.Context, g int64, n int) ([]qq.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []qq.Event{}
	for _, e := range s.events {
		if e.GroupID == g {
			out = append(out, e)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out, nil
}
func (s *Store) IsBotReply(_ context.Context, g int64, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.events {
		if e.GroupID == g && e.MessageID == id && e.Outbound && !e.Recalled {
			return true, nil
		}
	}
	return false, nil
}
func (s *Store) BeginAttempt(_ context.Context, a chat.Attempt) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.attempts[a.ID]; ok {
		return false, nil
	}
	s.attempts[a.ID] = a
	return true, nil
}
func (s *Store) UpdateAttempt(_ context.Context, a chat.Attempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attempts[a.ID].State != "confirmed" {
		s.attempts[a.ID] = a
	}
	return nil
}
func (s *Store) FinishAttempt(_ context.Context, a chat.Attempt, e qq.Event, noteIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attempts[a.ID].State != "sending" {
		return errors.New("send was not pending")
	}
	for _, old := range s.events {
		if old.ID == e.ID {
			return errors.New("outbound event already archived")
		}
	}
	a.State = "confirmed"
	s.attempts[a.ID] = a
	e.Seq = int64(len(s.events) + 1)
	s.events = append(s.events, e)
	booked := s.usage[a.ID]
	if booked == nil {
		booked = map[string]bool{}
		s.usage[a.ID] = booked
	}
	for _, id := range noteIDs {
		n, ok := s.notes[id]
		if !ok || n.GroupID != a.GroupID || booked[id] {
			continue
		}
		booked[id] = true
		if n.UsageVersion != 1 {
			n.UseCount = 0
		}
		n.UseCount++
		n.UsageVersion = 1
		at := e.At
		n.LastUsedAt = &at
		s.notes[id] = n
	}
	return nil
}

// Usage lists the note IDs booked by one confirmed attempt. Replays use it to
// verify that no other outcome writes the ledger.
func (s *Store) Usage(attemptID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for id := range s.usage[attemptID] {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Attempt exposes the stored attempt body for replay assertions.
func (s *Store) Attempt(id string) (chat.Attempt, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.attempts[id]
	return a, ok
}

func (s *Store) RecordLearningRun(_ context.Context, run memory.LearningRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs = append(s.runs, run)
	return nil
}

func (s *Store) LearningRuns() []memory.LearningRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]memory.LearningRun(nil), s.runs...)
}

func (s *Store) RecordMemoryEvent(_ context.Context, _ int64, kind string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memoryEvents = append(s.memoryEvents, kind)
	return nil
}

func (s *Store) MemoryEvents() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.memoryEvents...)
}
func (s *Store) RepliesSince(_ context.Context, g int64, since time.Time) (int, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	var last time.Time
	for _, a := range s.attempts {
		if a.GroupID == g && a.State == "confirmed" {
			if a.At.After(since) {
				count++
			}
			if a.At.After(last) {
				last = a.At
			}
		}
	}
	return count, last, nil
}
func (s *Store) ActiveNotes(_ context.Context, g int64, limit int) ([]memory.Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	out := []memory.Note{}
	for _, n := range s.notes {
		if n.GroupID == g && n.State == "active" && (n.Expires == nil || n.Expires.After(now)) && (n.ValidFrom == nil || !n.ValidFrom.After(now)) && (n.ValidTo == nil || n.ValidTo.After(now)) {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (s *Store) MemoryRevision(_ context.Context, g int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var revision int64
	for _, n := range s.notes {
		if n.GroupID == g {
			if n.MemoryRevision > 0 {
				revision += n.MemoryRevision
			} else {
				revision += n.Version
			}
			if revision == 0 {
				revision = 1
			}
		}
	}
	return revision, nil
}
func (s *Store) SaveTopics(_ context.Context, g int64, topics []conversation.TopicView, events []conversation.TopicEventView) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.topics[g] = append([]conversation.TopicView(nil), topics...)
	s.topicEvents[g] = append([]conversation.TopicEventView(nil), events...)
	return nil
}
func (s *Store) LoadTopics(_ context.Context, g int64) ([]conversation.TopicView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]conversation.TopicView(nil), s.topics[g]...), nil
}
func (s *Store) LoadTopicEvents(_ context.Context, g int64, limit int) ([]conversation.TopicEventView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	events := s.topicEvents[g]
	if len(events) > limit {
		events = events[len(events)-limit:]
	}
	out := append([]conversation.TopicEventView(nil), events...)
	for i := range out {
		out[i].TopicIDs = append([]string(nil), out[i].TopicIDs...)
	}
	return out, nil
}

func (s *Store) TopicIDs(_ context.Context, group int64, eventIDs []string) (map[string][]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wanted := map[string]bool{}
	for _, id := range eventIDs {
		wanted[id] = true
	}
	out := map[string][]string{}
	seen := map[string]map[string]bool{}
	for _, event := range s.topicEvents[group] {
		if !wanted[event.EventID] {
			continue
		}
		if seen[event.EventID] == nil {
			seen[event.EventID] = map[string]bool{}
		}
		topicIDs := event.TopicIDs
		if len(topicIDs) == 0 && event.TopicID != "" {
			topicIDs = []string{event.TopicID}
		}
		for _, topicID := range topicIDs {
			if topicID == "" || seen[event.EventID][topicID] {
				continue
			}
			seen[event.EventID][topicID] = true
			out[event.EventID] = append(out[event.EventID], topicID)
		}
	}
	return out, nil
}

func (s *Store) SummaryProgress(_ context.Context, g int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.summaryCursors[g], nil
}
func (s *Store) VectorNotes(_ context.Context, g int64, v []float32, version string, limit int, threshold float64) ([]memory.Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	type scored struct {
		n     memory.Note
		score float64
	}
	out := []scored{}
	for id, p := range s.vectors {
		n := s.notes[id]
		if n.GroupID == g && n.State == "active" && (n.Expires == nil || n.Expires.After(time.Now())) && (n.ValidFrom == nil || !n.ValidFrom.After(time.Now())) && (n.ValidTo == nil || n.ValidTo.After(time.Now())) && p.Version == n.Version && p.Model == version {
			if score := cosine(v, p.Vector); score > 1-threshold {
				out = append(out, scored{n, score})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].score > out[j].score })
	notes := []memory.Note{}
	for _, r := range out[:min(limit, len(out))] {
		notes = append(notes, r.n)
	}
	return notes, nil
}
func cosine(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var x, y, z float64
	for i, v := range a {
		x += float64(v * b[i])
		y += float64(v * v)
		z += float64(b[i] * b[i])
	}
	if y*z == 0 {
		return 0
	}
	return x / math.Sqrt(y*z)
}
func (s *Store) SaveNotes(_ context.Context, g int64, notes []memory.Note, p *memory.Progress) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p != nil && (p.GroupID != g || s.cursors[g] != p.After) {
		return errors.New("cursor changed")
	}
	for _, n := range notes {
		if n.GroupID != g || s.notes[n.ID].Version != n.Version {
			return errors.New("note revision changed")
		}
	}
	for _, n := range notes {
		if n.State != "active" {
			continue
		}
		for _, other := range s.notes {
			if other.GroupID != g || other.State != "forgotten" {
				continue
			}
			for _, incoming := range n.Evidence {
				for _, blocked := range other.Evidence {
					if incoming.EventID == blocked.EventID {
						return errors.New("forgotten source cannot be reintroduced")
					}
				}
			}
		}
	}
	for _, n := range notes {
		n.Version++
		if old, ok := s.notes[n.ID]; ok {
			n.UseCount = old.UseCount
			n.UsageVersion = old.UsageVersion
			n.LastUsedAt = old.LastUsedAt
		}
		if n.MemoryRevision < 1 {
			n.MemoryRevision = 1
		} else {
			n.MemoryRevision++
		}
		s.notes[n.ID] = n
		delete(s.vectors, n.ID)
	}
	if p != nil {
		s.cursors[g] = p.Until
	}
	return nil
}
func (s *Store) NotesByID(_ context.Context, g int64, ids []string) ([]memory.Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []memory.Note{}
	for _, id := range ids {
		if n, ok := s.notes[id]; ok && n.GroupID == g {
			out = append(out, n)
		}
	}
	return out, nil
}
func (s *Store) NotesByIdentity(_ context.Context, g int64, identities []memory.Identity) ([]memory.Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []memory.Note{}
	for _, identity := range identities {
		matches := []memory.Note{}
		for _, n := range s.notes {
			if n.GroupID == g && n.Subject == identity.Subject && n.Kind == identity.Kind && n.Key == identity.Key && (n.Kind != "episode" || n.TopicID == identity.TopicID) {
				matches = append(matches, n)
			}
		}
		sort.Slice(matches, func(i, j int) bool {
			if matches[i].Updated.Equal(matches[j].Updated) {
				return matches[i].ID < matches[j].ID
			}
			return matches[i].Updated.After(matches[j].Updated)
		})
		out = append(out, matches...)
	}
	return out, nil
}
func (s *Store) LearningBatch(_ context.Context, limit int) ([]qq.Event, memory.Progress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var group int64
	for _, e := range s.events {
		if e.Seq > s.cursors[e.GroupID] {
			group = e.GroupID
			break
		}
	}
	p := memory.Progress{GroupID: group, After: s.cursors[group]}
	out := []qq.Event{}
	for _, e := range s.events {
		if e.GroupID == group && e.Seq > p.After && len(out) < limit {
			out = append(out, e)
			p.Until = e.Seq
		}
	}
	return out, p, nil
}
func (s *Store) DeferLearning(context.Context, int64, time.Duration) error { return nil }
func (s *Store) PendingNotes(_ context.Context, version string, limit int) ([]memory.Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []memory.Note{}
	for id, n := range s.notes {
		p := s.vectors[id]
		if n.State == "active" && (n.Expires == nil || n.Expires.After(time.Now())) && (n.ValidFrom == nil || !n.ValidFrom.After(time.Now())) && (n.ValidTo == nil || n.ValidTo.After(time.Now())) && (p.Version != n.Version || p.Model != version) {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out[:min(len(out), limit)], nil
}
func (s *Store) IndexNote(_ context.Context, n memory.Note, v []float32, version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.notes[n.ID].Version == n.Version && s.notes[n.ID].State == "active" {
		s.vectors[n.ID] = projection{n.Version, version, append([]float32{}, v...)}
	}
	return nil
}

type Sender struct {
	mu       sync.Mutex
	Messages []string
	Error    error
}

func (s *Sender) Send(_ context.Context, g int64, text, quote, path string) (qq.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Error != nil {
		return qq.Receipt{}, s.Error
	}
	s.Messages = append(s.Messages, text+path)
	return qq.Receipt{MessageID: fmt.Sprintf("simulated-%d", len(s.Messages))}, nil
}
