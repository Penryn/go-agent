// Package runtime owns the human-presence message lifecycle.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/phlin/go-agent/internal/application/action"
	normalizersvc "github.com/phlin/go-agent/internal/application/normalizer"
	personasvc "github.com/phlin/go-agent/internal/application/persona"
	"github.com/phlin/go-agent/internal/application/presence/deliberation"
	groupactor "github.com/phlin/go-agent/internal/application/presence/group_actor"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

type Config struct {
	GroupWhitelist []int64
	SelfID         int64
}

type PerceptionSubmitter interface {
	Submit(presencedomain.EventRecord)
}

type ConfirmationObserver interface {
	ObserveConfirmation(groupID, userID int64, text string, at time.Time)
}

// EventObserverFunc 在每条入站事件后收到回调；错误只记日志。
type EventObserverFunc func(context.Context, conversationdomain.ConversationEvent) error

// TurnObserver closes the feedback loop between a realized action and future
// scheduling. Implementations own durable cooldown, persona, and reflection
// state; Runtime only invokes the narrow lifecycle hooks.
type TurnObserver interface {
	CanDeliberate(context.Context, int64, time.Time) (bool, error)
	AfterTurn(context.Context, conversationdomain.ContextSnapshot, policydomain.AutonomyDecision, replydomain.ActionReceipt) error
}

// InboundTurnObserver is an optional lifecycle hook for observers that keep
// state about consecutive bot turns. It is separate from TurnObserver so
// existing lightweight test and embedding implementations remain compatible.
type InboundTurnObserver interface {
	ObserveInbound(context.Context, conversationdomain.ConversationEvent) error
}

// pollInterval keeps actor reclamation responsive without a busy loop.
const pollInterval = 100 * time.Millisecond

type Outcome struct {
	Envelope conversationdomain.EventEnvelope   `json:"envelope"`
	Snapshot conversationdomain.ContextSnapshot `json:"snapshot"`
	Decision policydomain.AutonomyDecision      `json:"decision"`
	Plan     replydomain.ReplyPlan              `json:"plan"`
	Receipt  replydomain.ActionReceipt          `json:"receipt"`
}

type Runtime struct {
	normalizer     *normalizersvc.Service
	working        *groupactor.Manager
	deliberator    deliberation.Deliberator
	perception     PerceptionSubmitter
	confirmations  ConfirmationObserver
	turns          TurnObserver
	canon          *personasvc.CanonService
	executor       *action.Service
	eventObservers []EventObserverFunc
	whitelist      map[int64]struct{}
	selfID         int64

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (r *Runtime) SetConfirmationObserver(observer ConfirmationObserver) { r.confirmations = observer }

// SetCanonService enables post-delivery persistence for fictional persona
// facts declared in terminal reply tools.
func (r *Runtime) SetCanonService(service *personasvc.CanonService) { r.canon = service }

func (r *Runtime) AddEventObserver(observer EventObserverFunc) {
	if observer != nil {
		r.eventObservers = append(r.eventObservers, observer)
	}
}

func New(parent context.Context, normalizer *normalizersvc.Service, working *groupactor.Manager, deliberator deliberation.Deliberator, perception PerceptionSubmitter, turns TurnObserver, executor *action.Service, cfg Config) *Runtime {
	ctx, cancel := context.WithCancel(parent)
	r := &Runtime{
		normalizer:  normalizer,
		working:     working,
		deliberator: deliberator,
		perception:  perception,
		turns:       turns,
		executor:    executor,
		whitelist:   make(map[int64]struct{}, len(cfg.GroupWhitelist)),
		selfID:      cfg.SelfID,
		ctx:         ctx,
		cancel:      cancel,
	}
	for _, groupID := range cfg.GroupWhitelist {
		r.whitelist[groupID] = struct{}{}
	}
	r.wg.Add(1)
	go r.loop(pollInterval)
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
	if observer, ok := r.turns.(InboundTurnObserver); ok {
		if err := observer.ObserveInbound(ctx, envelope.Event); err != nil {
			return fmt.Errorf("observe inbound turn: %w", err)
		}
	}
	if r.confirmations != nil {
		r.confirmations.ObserveConfirmation(envelope.Event.GroupID, envelope.Event.UserID, envelope.Event.Text, envelope.ReceivedAt)
	}
	record := toEventRecord(envelope, presencedomain.OriginInbound)
	_, err = r.working.Observe(ctx, record)
	if err == nil && r.perception != nil {
		r.perception.Submit(record)
	}
	if err == nil {
		r.observeEvent(ctx, envelope.Event)
		if r.deliberator != nil {
			go func() {
				if _, err := r.deliberate(context.Background(), envelope); err != nil {
					slog.Debug("human runtime: asynchronous deliberation failed", "trace_id", envelope.TraceID, "err", err)
				}
			}()
		}
	}
	return err
}

// ProcessRawEvent is the synchronous replay surface for CLI and tests.
func (r *Runtime) ProcessRawEvent(ctx context.Context, payload []byte) (Outcome, error) {
	envelope, err := r.normalize(payload)
	if err != nil {
		return Outcome{}, fmt.Errorf("normalize event: %w", err)
	}
	if r.shouldIgnore(envelope) {
		return Outcome{Envelope: envelope, Decision: silentDecision(envelope.TraceID, "ignored")}, nil
	}
	_, hasInboundObserver := r.turns.(InboundTurnObserver)
	if r.turns != nil {
		allowed, err := r.turns.CanDeliberate(ctx, envelope.Event.GroupID, envelope.ReceivedAt)
		if err != nil {
			return Outcome{Envelope: envelope}, err
		}
		if !allowed && !hasInboundObserver {
			return r.silentBeforeModel(ctx, envelope, "turn_gate")
		}
	}
	if observer, ok := r.turns.(InboundTurnObserver); ok {
		if err := observer.ObserveInbound(ctx, envelope.Event); err != nil {
			return Outcome{Envelope: envelope}, fmt.Errorf("observe inbound turn: %w", err)
		}
	}
	record := toEventRecord(envelope, presencedomain.OriginInbound)
	_, err = r.working.ObserveReplay(ctx, record)
	if err != nil {
		return Outcome{}, fmt.Errorf("observe event: %w", err)
	}
	if r.perception != nil {
		r.perception.Submit(record)
	}
	if r.confirmations != nil {
		r.confirmations.ObserveConfirmation(envelope.Event.GroupID, envelope.Event.UserID, envelope.Event.Text, envelope.ReceivedAt)
	}
	r.observeEvent(ctx, envelope.Event)

	return r.deliberate(ctx, envelope)
}

func (r *Runtime) deliberate(ctx context.Context, envelope conversationdomain.EventEnvelope) (Outcome, error) {
	if r.turns != nil {
		allowed, err := r.turns.CanDeliberate(ctx, envelope.Event.GroupID, envelope.ReceivedAt)
		if err != nil {
			return Outcome{Envelope: envelope}, err
		}
		if !allowed {
			return r.silentBeforeModel(ctx, envelope, "turn_gate")
		}
	}
	if r.deliberator == nil {
		return Outcome{Envelope: envelope, Decision: silentDecision(envelope.TraceID, "delegated_to_decision_engine")}, nil
	}
	working, err := r.working.Snapshot(ctx, envelope.Event.GroupID)
	if err != nil {
		return Outcome{Envelope: envelope}, fmt.Errorf("snapshot event: %w", err)
	}
	result, err := r.deliberator.Deliberate(ctx, deliberation.Input{Envelope: envelope, Memory: working})
	if err != nil {
		return Outcome{Envelope: envelope}, err
	}
	if len(result.Plan.Bubbles) == 0 && result.Plan.FallbackText == "" && len(result.Plan.PlannedActions) == 0 {
		return Outcome{Envelope: envelope, Snapshot: result.Snapshot, Decision: result.Decision}, nil
	}
	var proposal personasvc.CanonProposal
	if r.canon != nil && len(result.Plan.ProposedPersonaFacts) > 0 {
		proposal, err = r.canon.PreparePlan(ctx, result.Snapshot.PersonaView, result.Plan.ProposedPersonaFacts, plannedReplyText(result.Plan), result.Decision.DecisionID)
		if err != nil {
			return Outcome{Envelope: envelope, Snapshot: result.Snapshot, Decision: result.Decision, Plan: result.Plan}, err
		}
		result.Plan.ProposedPersonaFacts = proposal.Candidates
	}
	if r.executor == nil {
		return Outcome{Envelope: envelope, Snapshot: result.Snapshot, Decision: result.Decision, Plan: result.Plan}, nil
	}
	receipt, err := r.executor.Execute(ctx, envelope.Event, result.Decision, result.Plan)
	outcome := Outcome{Envelope: envelope, Snapshot: result.Snapshot, Decision: result.Decision, Plan: result.Plan, Receipt: receipt}
	if proposal.ProposalID != "" {
		if receipt.Sent {
			if canonErr := r.canon.AfterDelivery(ctx, proposal, personasvc.CanonDelivery{
				GroupID: envelope.Event.GroupID, SelfID: envelope.SelfID, SourceEventID: receipt.ActionID, Text: receipt.DeliveredText,
			}); canonErr != nil {
				return outcome, canonErr
			}
		} else {
			_ = r.canon.AbortProposal(context.WithoutCancel(ctx), proposal)
		}
	}
	if err != nil {
		return outcome, err
	}
	if r.turns != nil {
		if err := r.turns.AfterTurn(ctx, result.Snapshot, result.Decision, receipt); err != nil {
			return outcome, err
		}
	}
	return outcome, nil
}

func (r *Runtime) loop(interval time.Duration) {
	defer r.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case now := <-ticker.C:
			r.working.PruneIdle(r.ctx, now)
		}
	}
}

func (r *Runtime) silentBeforeModel(ctx context.Context, envelope conversationdomain.EventEnvelope, reason string) (Outcome, error) {
	decision := silentDecision(envelope.TraceID, reason)
	plan := replydomain.ReplyPlan{PlanID: decision.DecisionID + "-plan", PlannedActions: []policydomain.DecisionAction{policydomain.ActionSilent}, SendMode: "silent"}
	var receipt replydomain.ActionReceipt
	var err error
	if r.executor != nil {
		receipt, err = r.executor.Execute(ctx, envelope.Event, decision, plan)
	} else {
		receipt.DropReason = "action_silent"
	}
	return Outcome{Envelope: envelope, Decision: decision, Plan: plan, Receipt: receipt}, err
}

func plannedReplyText(plan replydomain.ReplyPlan) string {
	if len(plan.Bubbles) > 0 {
		return strings.Join(plan.Bubbles, "")
	}
	if corrected, _ := plan.ActionParams["corrected_text"].(string); corrected != "" {
		return corrected
	}
	return plan.FallbackText
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
	if envelope.Event.Kind == conversationdomain.EventMeta || envelope.Event.GroupID <= 0 {
		return true
	}
	if envelope.Event.UserID != 0 && envelope.Event.UserID == envelope.SelfID {
		return true
	}
	if len(r.whitelist) == 0 {
		return false
	}
	_, ok := r.whitelist[envelope.Event.GroupID]
	return !ok
}

func toEventRecord(envelope conversationdomain.EventEnvelope, origin presencedomain.EventOrigin) presencedomain.EventRecord {
	return presencedomain.EventRecord{EventID: envelope.Event.EventID, GroupID: envelope.Event.GroupID, UserID: envelope.Event.UserID, Origin: origin, Timestamp: envelope.ReceivedAt, Event: envelope.Event, RawPayload: envelope.RawPayload}
}

func silentDecision(id, reason string) policydomain.AutonomyDecision {
	return policydomain.AutonomyDecision{DecisionID: id + "-decision", Action: policydomain.ActionSilent, ReasonCodes: []string{reason}, Confidence: 1}
}

func (r *Runtime) Close() error {
	r.cancel()
	r.wg.Wait()
	return nil
}
