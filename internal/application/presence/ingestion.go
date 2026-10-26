package presence

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
)

// SubmitRaw persists an event before starting a managed model turn. The
// inbound reader waits for ingestion, never for model planning or delivery.
func (r *Runtime) SubmitRaw(ctx context.Context, payload []byte) error {
	ctx, finish, err := r.beginOperation(ctx)
	if err != nil {
		return err
	}
	defer finish()
	envelope, err := r.normalize(payload)
	if err != nil {
		return err
	}
	if r.shouldIgnore(envelope) {
		return nil
	}
	if err := r.observeInbound(ctx, envelope); err != nil {
		return err
	}
	if r.deps.Deliberator == nil {
		return nil
	}
	turnCtx, finishTurn, err := r.beginTurn(r.ctx, envelope.Event.GroupID)
	if err != nil {
		return err
	}
	go func() {
		defer finishTurn()
		if _, err := r.deliberate(turnCtx, envelope); err != nil && !errors.Is(err, context.Canceled) {
			slog.Debug("presence: asynchronous deliberation failed", "trace_id", envelope.TraceID, "err", err)
		}
	}()
	return nil
}

// ProcessRawEvent runs the same event lifecycle synchronously for CLI and tests.
func (r *Runtime) ProcessRawEvent(ctx context.Context, payload []byte) (Outcome, error) {
	ctx, finish, err := r.beginOperation(ctx)
	if err != nil {
		return Outcome{}, err
	}
	defer finish()
	envelope, err := r.normalize(payload)
	if err != nil {
		return Outcome{}, fmt.Errorf("normalize event: %w", err)
	}
	if r.shouldIgnore(envelope) {
		return Outcome{Envelope: envelope, Decision: silentDecision(envelope.TraceID, "ignored")}, nil
	}
	if err := r.observeInbound(ctx, envelope); err != nil {
		return Outcome{Envelope: envelope}, err
	}
	turnCtx, finishTurn, err := r.beginTurn(ctx, envelope.Event.GroupID)
	if err != nil {
		return Outcome{Envelope: envelope}, err
	}
	defer finishTurn()
	return r.deliberate(turnCtx, envelope)
}

// observeInbound is the only ingestion pipeline. A turn gate suppresses model
// planning and output, never the source fact or its projections.
func (r *Runtime) observeInbound(ctx context.Context, envelope conversationdomain.EventEnvelope) error {
	if r.deps.Executor != nil {
		r.deps.Executor.CancelQueued(envelope.Event.GroupID)
	}
	record := toEventRecord(envelope, presencedomain.OriginInbound)
	if _, err := r.deps.WorkingMemory.Observe(ctx, record); err != nil {
		return fmt.Errorf("observe event: %w", err)
	}
	// Derived interaction state may only advance after the source event is durable.
	if r.deps.Turns != nil {
		if err := r.deps.Turns.ObserveInbound(ctx, envelope.Event); err != nil {
			return fmt.Errorf("observe inbound turn: %w", err)
		}
	}
	if r.deps.Confirmations != nil {
		r.deps.Confirmations.ObserveConfirmation(envelope.Event.GroupID, envelope.Event.UserID, envelope.Event.Text, envelope.ReceivedAt)
	}
	if r.deps.Feedback != nil {
		if err := r.deps.Feedback.ObserveInbound(ctx, envelope.Event); err != nil {
			slog.Warn("presence: feedback observation failed", "group_id", envelope.Event.GroupID, "event_id", envelope.Event.EventID, "err", err)
		}
	}
	if r.deps.Perception != nil {
		r.deps.Perception.Submit(record)
	}
	r.observeEvent(ctx, envelope.Event)
	return nil
}

func (r *Runtime) observeEvent(ctx context.Context, event conversationdomain.ConversationEvent) {
	for _, observer := range r.deps.EventObservers {
		if err := observer(ctx, event); err != nil {
			slog.Warn("presence: event observer failed", "group_id", event.GroupID, "user_id", event.UserID, "err", err)
		}
	}
}

func (r *Runtime) normalize(payload []byte) (conversationdomain.EventEnvelope, error) {
	if r.deps.Normalizer == nil {
		return conversationdomain.EventEnvelope{}, errors.New("presence: normalizer is nil")
	}
	return r.deps.Normalizer.Normalize(payload)
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
