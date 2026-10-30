package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/phlin/go-agent/internal/chat"
	"github.com/phlin/go-agent/internal/qq"
	"github.com/phlin/go-agent/internal/store"
)

const (
	reconcileInterval = 30 * time.Second
	reconcileBackoff  = time.Minute
	reconcileBatch    = 8
)

// reconciler closes unknown sends locally: it verifies the platform message
// with the persisted receipt and finishes the archive idempotently. Every
// compensation path retries storage only — a compensating flow never calls a
// send API.
type reconciler struct {
	qq      *qq.Client
	store   *store.Store
	runtime *chat.Runtime
	self    int64
}

func (r *reconciler) Tick(ctx context.Context) error {
	due, err := r.store.ReconcileDue(ctx, reconcileBatch)
	if err != nil {
		return err
	}
	for _, candidate := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.reconcile(ctx, candidate)
	}
	return nil
}

func (r *reconciler) reconcile(ctx context.Context, candidate store.ReconcileCandidate) {
	attempt := candidate.Attempt
	if attempt.ID == "" || attempt.GroupID == 0 {
		return
	}
	if candidate.Kind == "action" {
		// The platform already confirmed the action and only the local
		// archive failed: retry the confirmation transaction directly.
		record := r.runtime.ActionRecordFor(attempt.Reply, attempt.GroupID)
		if err := r.store.ConfirmAction(ctx, attempt, record, attempt.Retrieval.Selected); err != nil {
			r.deferReconcile(ctx, attempt.ID, err)
			return
		}
		r.runtime.ApplyReconciledAction(record)
		return
	}
	facts, err := r.qq.FetchMessage(ctx, candidate.MessageID)
	if err != nil {
		var sendErr *qq.SendError
		if errors.As(err, &sendErr) && sendErr.Definite {
			// The platform definitively contradicts the stored send.
			if failErr := r.store.FailReconcile(ctx, attempt.ID, "platform rejected the stored message id"); failErr != nil {
				slog.Error("fail reconcile", "attempt", attempt.ID, "error", failErr)
			}
			return
		}
		r.deferReconcile(ctx, attempt.ID, err)
		return
	}
	if facts.GroupID != attempt.GroupID || facts.SenderID != r.self {
		// A foreign message proves nothing about this attempt.
		if failErr := r.store.FailReconcile(ctx, attempt.ID, "message identity does not match the attempt"); failErr != nil {
			slog.Error("fail reconcile", "attempt", attempt.ID, "error", failErr)
		}
		return
	}
	if err := r.store.ReconcileOutbound(ctx, attempt, r.outboundEvent(attempt, candidate.MessageID), attempt.Retrieval.Selected); err != nil {
		r.deferReconcile(ctx, attempt.ID, err)
		return
	}
	topicID := ""
	if attempt.Reply.Plan != nil {
		topicID = attempt.Reply.Plan.TopicID
	}
	r.runtime.ApplyReconciledMessage(r.outboundEvent(attempt, candidate.MessageID), topicID)
}

func (r *reconciler) deferReconcile(ctx context.Context, attemptID string, cause error) {
	if err := r.store.DeferReconcile(ctx, attemptID, cause.Error(), time.Now().Add(reconcileBackoff)); err != nil {
		slog.Error("defer reconcile", "attempt", attemptID, "error", err)
	}
	slog.Warn("reconcile deferred", "attempt", attemptID, "error", cause, "retry_in", reconcileBackoff)
}

// outboundEvent rebuilds the deterministic outbound event of an attempt. The
// event id derives from the attempt id, so archiving is idempotent across
// retries. ponytail: sticker descriptions lived only in the send-time
// snapshot; the reconciled archive keeps the plain marker.
func (r *reconciler) outboundEvent(attempt chat.Attempt, messageID string) qq.Event {
	hash := sha256.Sum256([]byte(attempt.ID))
	text := attempt.Reply.Text
	if attempt.Reply.Action == "sticker" {
		text = "[表情包]"
	}
	return qq.Event{ID: fmt.Sprintf("out:%x", hash[:12]), MessageID: messageID, GroupID: attempt.GroupID, UserID: r.self, SelfID: r.self, Name: r.runtime.Persona.Name, Text: text, ReplyTo: attempt.Reply.Quote, Outbound: true, At: time.Now()}
}
