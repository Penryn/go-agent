// Package runtime owns the human-presence message lifecycle.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
	"github.com/phlin/go-agent/internal/humanbot/runtime/deliberation"
	groupactor "github.com/phlin/go-agent/internal/humanbot/runtime/group_actor"
	"github.com/phlin/go-agent/internal/services/action"
	normalizersvc "github.com/phlin/go-agent/internal/services/normalizer"
)

type Config struct {
	PollInterval      time.Duration
	MinCandidateScore float64
	JobTimeout        time.Duration
	WorkerCount       int
	GroupWhitelist    []int64
	SelfID            int64
}

type PerceptionSubmitter interface {
	Submit(humandomain.EventRecord)
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

func DefaultConfig() Config {
	return Config{PollInterval: 100 * time.Millisecond, MinCandidateScore: 0.5, JobTimeout: 120 * time.Second, WorkerCount: 4}
}

type Outcome struct {
	Envelope  conversationdomain.EventEnvelope   `json:"envelope"`
	Snapshot  conversationdomain.ContextSnapshot `json:"snapshot"`
	Candidate humandomain.ThoughtCandidate       `json:"candidate"`
	Decision  policydomain.AutonomyDecision      `json:"decision"`
	Plan      replydomain.ReplyPlan              `json:"plan"`
	Receipt   replydomain.ActionReceipt          `json:"receipt"`
}

type Runtime struct {
	normalizer             *normalizersvc.Service
	working                *groupactor.Manager
	deliberator            deliberation.Deliberator
	perception             PerceptionSubmitter
	turns                  TurnObserver
	executor               *action.Service
	thoughts               ports.ThoughtStore
	eventObservers         []EventObserverFunc
	completedTurnObservers []CompletedTurnObserverFunc
	whitelist              map[int64]struct{}
	selfID                 int64
	minCandidateScore      float64

	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	workersWG sync.WaitGroup
	jobs      chan candidateJob
	locks     sync.Map
}

type candidateJob struct {
	groupID   int64
	candidate humandomain.ThoughtCandidate
	timeout   time.Duration
}

// SetThoughtStore enables durable, concise deliberation records. It is
// optional so replay and in-memory callers can keep the runtime lightweight.
func (r *Runtime) SetThoughtStore(store ports.ThoughtStore) { r.thoughts = store }

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
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaults.PollInterval
	}
	if cfg.MinCandidateScore <= 0 {
		cfg.MinCandidateScore = defaults.MinCandidateScore
	}
	if cfg.JobTimeout <= 0 {
		cfg.JobTimeout = defaults.JobTimeout
	}
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = defaults.WorkerCount
	}
	ctx, cancel := context.WithCancel(parent)
	r := &Runtime{
		normalizer:        normalizer,
		working:           working,
		deliberator:       deliberator,
		perception:        perception,
		turns:             turns,
		executor:          executor,
		whitelist:         make(map[int64]struct{}, len(cfg.GroupWhitelist)),
		selfID:            cfg.SelfID,
		minCandidateScore: cfg.MinCandidateScore,
		ctx:               ctx,
		cancel:            cancel,
		jobs:              make(chan candidateJob, cfg.WorkerCount*2),
	}
	for _, groupID := range cfg.GroupWhitelist {
		r.whitelist[groupID] = struct{}{}
	}
	r.wg.Add(1)
	for i := 0; i < cfg.WorkerCount; i++ {
		r.workersWG.Add(1)
		go r.worker()
	}
	go r.loop(cfg.PollInterval, cfg.MinCandidateScore, cfg.JobTimeout)
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
	record := toEventRecord(envelope, humandomain.OriginInbound)
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
func (r *Runtime) ScheduleCandidate(ctx context.Context, groupID int64, candidate humandomain.ThoughtCandidate) error {
	if r == nil || r.working == nil {
		return errors.New("human runtime: working memory is nil")
	}
	return r.working.EnqueueCandidate(ctx, groupID, candidate)
}

// ProcessRawEvent is the synchronous replay surface. It records the event,
// then immediately deliberates its highest-value candidate for CLI/tests.
func (r *Runtime) ProcessRawEvent(ctx context.Context, payload []byte) (Outcome, error) {
	envelope, err := r.normalize(payload)
	if err != nil {
		return Outcome{}, fmt.Errorf("normalize event: %w", err)
	}
	if r.shouldIgnore(envelope) {
		return Outcome{Envelope: envelope, Decision: silentDecision(envelope.TraceID, "ignored")}, nil
	}
	record := toEventRecord(envelope, humandomain.OriginInbound)
	memory, err := r.working.Observe(ctx, record)
	if err != nil {
		return Outcome{}, fmt.Errorf("observe event: %w", err)
	}
	if r.perception != nil {
		r.perception.Submit(record)
	}
	r.observeEvent(ctx, envelope.Event)
	var candidate humandomain.ThoughtCandidate
	for i := len(memory.Candidates) - 1; i >= 0; i-- {
		if slices.Contains(memory.Candidates[i].SourceEventIDs, envelope.Event.EventID) {
			candidate = memory.Candidates[i]
			break
		}
	}
	if candidate.CandidateID == "" {
		return Outcome{Envelope: envelope, Decision: silentDecision(envelope.TraceID, "no_candidate")}, nil
	}
	if candidate.Score < r.minCandidateScore {
		_ = r.working.Complete(ctx, envelope.Event.GroupID, candidate.CandidateID)
		return Outcome{
			Envelope:  envelope,
			Candidate: candidate,
			Decision:  silentDecision(envelope.TraceID, "candidate_below_score"),
		}, nil
	}
	outcome, err := r.process(ctx, envelope, candidate)
	_ = r.working.Complete(ctx, envelope.Event.GroupID, candidate.CandidateID)
	return outcome, err
}

func (r *Runtime) loop(interval time.Duration, minScore float64, timeout time.Duration) {
	defer r.wg.Done()
	defer close(r.jobs)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case now := <-ticker.C:
			r.working.PruneIdle(r.ctx, now)
			for _, groupID := range r.working.GroupIDs() {
				if r.turns != nil {
					allowed, err := r.turns.CanDeliberate(r.ctx, groupID, now)
					if err != nil || !allowed {
						if err != nil {
							slog.Warn("human runtime: presence check failed", "group_id", groupID, "err", err)
						}
						continue
					}
				}
				candidate, ok, err := r.working.ClaimDue(r.ctx, groupID, now, minScore)
				if err != nil || !ok {
					continue
				}
				job := candidateJob{groupID: groupID, candidate: candidate, timeout: timeout}
				select {
				case r.jobs <- job:
				case <-r.ctx.Done():
					_ = r.working.Complete(context.Background(), groupID, candidate.CandidateID)
					return
				default:
					// A claimed candidate must reach a terminal state even when the
					// bounded queue is saturated.
					_ = r.working.Complete(context.Background(), groupID, candidate.CandidateID)
					slog.Warn("human runtime: candidate queue full", "group_id", groupID, "candidate_id", candidate.CandidateID)
				}
			}
		}
	}
}

func (r *Runtime) worker() {
	defer r.workersWG.Done()
	for {
		select {
		case <-r.ctx.Done():
			// The scheduler closes jobs after it stops producing. Drain any
			// already-claimed work so shutdown cannot leave accepted candidates
			// leased forever.
			for job := range r.jobs {
				_ = r.working.Complete(context.Background(), job.groupID, job.candidate.CandidateID)
			}
			return
		case job, ok := <-r.jobs:
			if !ok {
				return
			}
			lock := r.groupLock(job.groupID)
			lock.Lock()
			ctx, cancel := context.WithTimeout(r.ctx, job.timeout)
			if _, err := r.processCandidate(ctx, job.groupID, job.candidate); err != nil {
				slog.Warn("human runtime: candidate failed", "group_id", job.groupID, "candidate_id", job.candidate.CandidateID, "err", err)
			}
			cancel()
			lock.Unlock()
		}
	}
}

func (r *Runtime) processCandidate(ctx context.Context, groupID int64, candidate humandomain.ThoughtCandidate) (Outcome, error) {
	defer func() {
		// Completion is idempotent and must happen on stale, expired, and error
		// paths so accepted work cannot remain leased forever.
		_ = r.working.Complete(context.Background(), groupID, candidate.CandidateID)
	}()
	if ok, err := r.working.CanExecute(ctx, groupID, candidate.CandidateID, time.Now()); err != nil {
		return Outcome{}, err
	} else if !ok {
		return Outcome{Candidate: candidate, Decision: silentDecision(candidate.CandidateID, "candidate_stale")}, nil
	}
	memory, err := r.working.Snapshot(ctx, groupID)
	if err != nil {
		return Outcome{}, err
	}
	var event conversationdomain.ConversationEvent
	for _, record := range memory.RecentTail {
		for _, eventID := range candidate.SourceEventIDs {
			if record.EventID == eventID {
				event = record.Event
			}
		}
	}
	if event.EventID == "" {
		_ = r.working.Complete(ctx, groupID, candidate.CandidateID)
		return Outcome{}, errors.New("candidate source event no longer in working memory")
	}
	envelope := conversationdomain.EventEnvelope{Source: "humanbot", SelfID: r.selfID, ReceivedAt: time.Now(), Event: event, TraceID: event.EventID, CorrelationID: event.MessageID}
	outcome, err := r.processWithValidation(ctx, envelope, candidate, func(checkCtx context.Context) (bool, error) {
		return r.working.CanExecute(checkCtx, groupID, candidate.CandidateID, time.Now())
	})
	return outcome, err
}

func (r *Runtime) process(ctx context.Context, envelope conversationdomain.EventEnvelope, candidate humandomain.ThoughtCandidate) (Outcome, error) {
	return r.processWithValidation(ctx, envelope, candidate, nil)
}

func (r *Runtime) processWithValidation(ctx context.Context, envelope conversationdomain.EventEnvelope, candidate humandomain.ThoughtCandidate, validate func(context.Context) (bool, error)) (Outcome, error) {
	memory, err := r.working.Snapshot(ctx, envelope.Event.GroupID)
	if err != nil {
		return Outcome{}, fmt.Errorf("load working memory: %w", err)
	}
	result, err := r.deliberator.Deliberate(ctx, deliberation.Input{Envelope: envelope, Candidate: candidate, Memory: memory})
	if err != nil {
		return Outcome{}, fmt.Errorf("deliberate response: %w", err)
	}
	snapshot, decision, plan := result.Snapshot, result.Decision, result.Plan
	if validate != nil {
		valid, err := validate(ctx)
		if err != nil {
			return Outcome{}, fmt.Errorf("validate candidate before action: %w", err)
		}
		if !valid {
			return Outcome{Envelope: envelope, Snapshot: snapshot, Candidate: candidate, Decision: silentDecision(envelope.TraceID, "candidate_stale"), Plan: plan}, nil
		}
	}
	receipt, err := r.executor.Execute(ctx, envelope.Event, decision, plan)
	if err != nil {
		return Outcome{}, fmt.Errorf("realize response: %w", err)
	}
	if r.thoughts != nil {
		outcome := "silent"
		if receipt.Sent {
			outcome = "sent"
		}
		thought := result.Thought
		if thought.ThoughtID == "" {
			thought.ThoughtID = envelope.TraceID + "-thought"
		}
		thought.CandidateID = candidate.CandidateID
		thought.GroupID = envelope.Event.GroupID
		thought.EventID = envelope.Event.EventID
		thought.ChosenAction = string(decision.Action)
		thought.Outcome = outcome
		if thought.CreatedAt.IsZero() {
			thought.CreatedAt = time.Now()
		}
		if err := r.thoughts.SaveThought(ctx, thought); err != nil {
			slog.Warn("human runtime: record thought failed", "group_id", envelope.Event.GroupID, "err", err)
		}
	}
	if r.turns != nil {
		if err := r.turns.AfterTurn(ctx, snapshot, decision, receipt); err != nil {
			slog.Warn("human runtime: record turn failed", "group_id", envelope.Event.GroupID, "decision_id", decision.DecisionID, "err", err)
		}
	}
	for _, observer := range r.completedTurnObservers {
		if err := observer(ctx, snapshot, receipt); err != nil {
			slog.Warn("human runtime: completed turn observer failed", "group_id", envelope.Event.GroupID, "err", err)
		}
	}
	return Outcome{Envelope: envelope, Snapshot: snapshot, Candidate: candidate, Decision: decision, Plan: plan, Receipt: receipt}, nil
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
	if envelope.Event.UserID != 0 && envelope.Event.UserID == envelope.SelfID {
		return true
	}
	if len(r.whitelist) == 0 || envelope.Event.GroupID == 0 {
		return false
	}
	_, ok := r.whitelist[envelope.Event.GroupID]
	return !ok
}

func toEventRecord(envelope conversationdomain.EventEnvelope, origin humandomain.EventOrigin) humandomain.EventRecord {
	return humandomain.EventRecord{EventID: envelope.Event.EventID, GroupID: envelope.Event.GroupID, UserID: envelope.Event.UserID, Origin: origin, Timestamp: envelope.ReceivedAt, Event: envelope.Event, RawPayload: envelope.RawPayload}
}

func silentDecision(id, reason string) policydomain.AutonomyDecision {
	return policydomain.AutonomyDecision{DecisionID: id + "-decision", Action: policydomain.ActionSilent, StateBefore: policydomain.StateObserving, StateAfter: policydomain.StateObserving, ReasonCodes: []string{reason}, Explain: map[string]float64{reason: 1}, Confidence: 1}
}

func (r *Runtime) groupLock(groupID int64) *sync.Mutex {
	lock, _ := r.locks.LoadOrStore(groupID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func (r *Runtime) Close() error {
	r.cancel()
	r.wg.Wait()
	r.workersWG.Wait()
	return nil
}
