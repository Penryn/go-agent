// Package runtime owns the human-presence message lifecycle.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
	"github.com/phlin/go-agent/internal/humanbot/runtime/deliberation"
	groupactor "github.com/phlin/go-agent/internal/humanbot/runtime/group_actor"
	presencesvc "github.com/phlin/go-agent/internal/humanbot/runtime/presence"
	"github.com/phlin/go-agent/internal/services/action"
	normalizersvc "github.com/phlin/go-agent/internal/services/normalizer"
)

type Config struct {
	PollInterval      time.Duration
	MinCandidateScore float64
	JobTimeout        time.Duration
	GroupWhitelist    []int64
	SelfID            int64
}

type PerceptionSubmitter interface {
	Submit(humandomain.EventRecord)
}

func DefaultConfig() Config {
	return Config{PollInterval: 100 * time.Millisecond, MinCandidateScore: 0.5, JobTimeout: 120 * time.Second}
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
	normalizer  *normalizersvc.Service
	working     *groupactor.Manager
	deliberator deliberation.Deliberator
	perception  PerceptionSubmitter
	executor    *action.Service
	scheduler   presencesvc.Scheduler
	whitelist   map[int64]struct{}
	selfID      int64

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	jobsWG sync.WaitGroup
	locks  sync.Map
}

func New(parent context.Context, normalizer *normalizersvc.Service, working *groupactor.Manager, deliberator deliberation.Deliberator, perception PerceptionSubmitter, executor *action.Service, cfg Config) *Runtime {
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
	ctx, cancel := context.WithCancel(parent)
	r := &Runtime{
		normalizer:  normalizer,
		working:     working,
		deliberator: deliberator,
		perception:  perception,
		executor:    executor,
		scheduler:   presencesvc.NewScheduler(1),
		whitelist:   make(map[int64]struct{}, len(cfg.GroupWhitelist)),
		selfID:      cfg.SelfID,
		ctx:         ctx,
		cancel:      cancel,
	}
	for _, groupID := range cfg.GroupWhitelist {
		r.whitelist[groupID] = struct{}{}
	}
	r.wg.Add(1)
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
	record := toEventRecord(envelope, humandomain.OriginInbound)
	_, err = r.working.Observe(ctx, record)
	if err == nil && r.perception != nil {
		r.perception.Submit(record)
	}
	return err
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
	var candidate humandomain.ThoughtCandidate
	for i := len(memory.Candidates) - 1; i >= 0; i-- {
		if contains(memory.Candidates[i].SourceEventIDs, envelope.Event.EventID) {
			candidate = memory.Candidates[i]
			break
		}
	}
	if candidate.CandidateID == "" {
		return Outcome{Envelope: envelope, Decision: silentDecision(envelope.TraceID, "no_candidate")}, nil
	}
	outcome, err := r.process(ctx, envelope, candidate)
	_ = r.working.Complete(ctx, envelope.Event.GroupID, candidate.CandidateID)
	return outcome, err
}

func (r *Runtime) loop(interval time.Duration, minScore float64, timeout time.Duration) {
	defer r.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case now := <-ticker.C:
			for _, groupID := range r.working.GroupIDs() {
				candidate, ok, err := r.working.ClaimDue(r.ctx, groupID, now, minScore)
				if err != nil || !ok {
					continue
				}
				r.jobsWG.Add(1)
				go func(groupID int64, candidate humandomain.ThoughtCandidate) {
					defer r.jobsWG.Done()
					lock := r.groupLock(groupID)
					lock.Lock()
					defer lock.Unlock()
					ctx, cancel := context.WithTimeout(r.ctx, timeout)
					defer cancel()
					if _, err := r.processCandidate(ctx, groupID, candidate); err != nil {
						slog.Warn("human runtime: candidate failed", "group_id", groupID, "candidate_id", candidate.CandidateID, "err", err)
					}
				}(groupID, candidate)
			}
		}
	}
}

func (r *Runtime) processCandidate(ctx context.Context, groupID int64, candidate humandomain.ThoughtCandidate) (Outcome, error) {
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
	outcome, err := r.process(ctx, envelope, candidate)
	_ = r.working.Complete(ctx, groupID, candidate.CandidateID)
	return outcome, err
}

func (r *Runtime) process(ctx context.Context, envelope conversationdomain.EventEnvelope, candidate humandomain.ThoughtCandidate) (Outcome, error) {
	memory, err := r.working.Snapshot(ctx, envelope.Event.GroupID)
	if err != nil {
		return Outcome{}, fmt.Errorf("load working memory: %w", err)
	}
	result, err := r.deliberator.Deliberate(ctx, deliberation.Input{Envelope: envelope, Candidate: candidate, Memory: memory})
	if err != nil {
		return Outcome{}, fmt.Errorf("deliberate response: %w", err)
	}
	snapshot, decision, plan := result.Snapshot, result.Decision, result.Plan
	receipt, err := r.executor.Execute(ctx, envelope.Event, decision, plan)
	if err != nil {
		return Outcome{}, fmt.Errorf("realize response: %w", err)
	}
	return Outcome{Envelope: envelope, Snapshot: snapshot, Candidate: candidate, Decision: decision, Plan: plan, Receipt: receipt}, nil
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

func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
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
	r.jobsWG.Wait()
	return nil
}
