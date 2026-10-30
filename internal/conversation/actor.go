package conversation

import (
	"context"
	"sync"
	"time"

	"github.com/phlin/go-agent/internal/qq"
)

type GroupActor interface {
	Submit(context.Context, qq.Event) error
	Snapshot(context.Context) (Snapshot, error)
	Close()
}

type Snapshot struct {
	GroupID       int64
	Version       uint64
	LastSeq       int64
	ActiveTopicID string
	Topics        []TopicView
	Participants  []int64
	LastBotAt     time.Time
	Pending       []PendingQuestion
}
type TopicView struct {
	ID, Summary       string
	Participants      []int64
	FirstSeq, LastSeq int64
	Status            string
	Revision          int64
	SummaryRevision   int64
	LastActiveAt      time.Time
	// BotMessageAt and BotActionAt split bot participation by source so
	// continuation can be closed per source without losing the action facts.
	BotMessageAt time.Time
	BotActionAt  time.Time
}
type PendingQuestion struct {
	EventID, Text string
	At            time.Time
}

type ActorOptions struct {
	Merge   time.Duration
	MaxWait time.Duration
	Process func(context.Context, []qq.Event, uint64) error
}

// Actor serializes short-lived state updates for a single group. Process runs
// after the actor has released its lock, so model and storage calls never
// block event admission.
type Actor struct {
	groupID  int64
	options  ActorOptions
	ctx      context.Context
	cancel   context.CancelFunc
	wake     chan struct{}
	done     chan struct{}
	mu       sync.Mutex
	pending  []qq.Event
	snapshot Snapshot
	closed   bool
}

func NewActor(parent context.Context, groupID int64, options ActorOptions) *Actor {
	ctx, cancel := context.WithCancel(parent)
	a := &Actor{groupID: groupID, options: options, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), done: make(chan struct{}), snapshot: Snapshot{GroupID: groupID}}
	go a.loop()
	return a
}

func (a *Actor) Submit(ctx context.Context, event qq.Event) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return context.Canceled
	}
	a.snapshot.Version++
	if event.Seq > a.snapshot.LastSeq {
		a.snapshot.LastSeq = event.Seq
	}
	a.pending = append(a.pending, event)
	a.mu.Unlock()
	select {
	case a.wake <- struct{}{}:
	default:
	}
	return nil
}

func (a *Actor) Snapshot(ctx context.Context) (Snapshot, error) {
	select {
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	default:
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.snapshot
	s.Topics = append([]TopicView(nil), a.snapshot.Topics...)
	s.Participants = append([]int64(nil), a.snapshot.Participants...)
	s.Pending = append([]PendingQuestion(nil), a.snapshot.Pending...)
	return s, nil
}

func (a *Actor) UpdateSnapshot(update func(*Snapshot)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.closed {
		update(&a.snapshot)
		a.snapshot.Version++
	}
}

func (a *Actor) Close() {
	a.mu.Lock()
	if !a.closed {
		a.closed = true
		a.cancel()
	}
	a.mu.Unlock()
	<-a.done
}

func (a *Actor) loop() {
	defer close(a.done)
	merge := a.options.Merge
	if merge <= 0 {
		merge = time.Millisecond
	}
	for {
		a.mu.Lock()
		if len(a.pending) == 0 {
			a.mu.Unlock()
			select {
			case <-a.ctx.Done():
				return
			case <-a.wake:
				continue
			}
		}
		batch := append([]qq.Event(nil), a.pending...)
		a.pending = nil
		version := a.snapshot.Version
		a.mu.Unlock()
		timer := time.NewTimer(merge)
		select {
		case <-timer.C:
		case <-a.ctx.Done():
			timer.Stop()
			return
		}
		if a.options.Process != nil {
			_ = a.options.Process(a.ctx, batch, version)
		}
	}
}
