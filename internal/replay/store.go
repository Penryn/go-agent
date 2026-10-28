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
	notes          map[string]memory.Note
	vectors        map[string]projection
	cursors        map[int64]int64
	topics         map[int64][]conversation.TopicView
	summaryCursors map[int64]int64
}

func NewStore() *Store {
	return &Store{attempts: map[string]chat.Attempt{}, notes: map[string]memory.Note{}, vectors: map[string]projection{}, cursors: map[int64]int64{}, topics: map[int64][]conversation.TopicView{}, summaryCursors: map[int64]int64{}}
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
func (s *Store) FinishAttempt(_ context.Context, a chat.Attempt, e qq.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attempts[a.ID].State != "sending" {
		return errors.New("send was not pending")
	}
	s.attempts[a.ID] = a
	e.Seq = int64(len(s.events) + 1)
	s.events = append(s.events, e)
	return nil
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
func (s *Store) Notes(_ context.Context, g int64, limit int) ([]memory.Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []memory.Note{}
	for _, n := range s.notes {
		if n.GroupID == g {
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
func (s *Store) FlushNoteUsage(_ context.Context, usage map[string]int64, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, count := range usage {
		if n, ok := s.notes[id]; ok && n.State == "active" {
			n.UseCount += count
			n.LastUsedAt = &at
			s.notes[id] = n
		}
	}
	return nil
}
func (s *Store) SaveTopics(_ context.Context, g int64, topics []conversation.TopicView, summaryAfterSeq int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.topics[g] = append([]conversation.TopicView(nil), topics...)
	if summaryAfterSeq > s.summaryCursors[g] {
		s.summaryCursors[g] = summaryAfterSeq
	}
	return nil
}
func (s *Store) LoadTopics(_ context.Context, g int64) ([]conversation.TopicView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]conversation.TopicView(nil), s.topics[g]...), nil
}
func (s *Store) SummaryProgress(_ context.Context, g int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.summaryCursors[g], nil
}
func (s *Store) VectorNotes(_ context.Context, g int64, v []float32, version string, limit int) ([]memory.Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	type scored struct {
		n     memory.Note
		score float64
	}
	out := []scored{}
	for id, p := range s.vectors {
		n := s.notes[id]
		if n.GroupID == g && n.State == "active" && (n.Expires == nil || n.Expires.After(time.Now())) && p.Version == n.Version && p.Model == version {
			if score := cosine(v, p.Vector); score > 0.4 {
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
		n.Version++
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
func (s *Store) DeferLearning(context.Context, int64) error { return nil }
func (s *Store) PendingNotes(_ context.Context, version string, limit int) ([]memory.Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []memory.Note{}
	for id, n := range s.notes {
		p := s.vectors[id]
		if n.State == "active" && (n.Expires == nil || n.Expires.After(time.Now())) && (p.Version != n.Version || p.Model != version) {
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
