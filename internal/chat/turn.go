package chat

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	ctxsnapshot "github.com/phlin/go-agent/internal/context"
	"github.com/phlin/go-agent/internal/delivery"
	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/model"
	"github.com/phlin/go-agent/internal/qq"
)

func (r *Runtime) generateTurn(ctx context.Context, attempt Attempt, snapshot ctxsnapshot.ContextSnapshot) Attempt {
	if r.Model == nil {
		attempt.State = "silent"
		attempt.Reply.Reason = "model_unavailable"
		return attempt
	}
	input := map[string]any{
		"persona":                   r.Persona,
		"now":                       time.Now().Format(time.RFC3339),
		"history":                   snapshot.History,
		"batch":                     snapshot.Batch,
		"conversation_context":      snapshot.Conversation,
		"notes":                     snapshot.Notes,
		"sticker_candidates":        snapshot.StickerAssets,
		"reaction_emoji_candidates": ReactionEmojis,
		"loaded_image_count":        len(snapshot.Images),
	}
	var result model.Result
	var err error
	if gateway, ok := r.Model.(model.ModelGateway); ok {
		result, err = gateway.GenerateRequest(ctx, model.GenerateRequest{PromptVersion: "chat-v2", System: Prompt, Input: input, Images: snapshot.Images, Priority: model.PriorityInteractive, GroupID: attempt.GroupID, AttemptID: attempt.ID})
	} else {
		result, err = r.Model.Generate(ctx, Prompt, input, snapshot.Images)
	}
	if err != nil {
		attempt.State = "failed"
		attempt.Error = err.Error()
		return attempt
	}
	attempt.Usage = result.Usage
	if err := model.JSON(result.Content, &attempt.Reply); err != nil {
		attempt.State = "failed"
		attempt.Error = "invalid reply structure"
		return attempt
	}
	normalizeReply(&attempt.Reply)
	visible := combineEvents(snapshot.History, snapshot.Batch)
	if err := validate(attempt.Reply, visible, snapshot.StickerAssets, observationFromSnapshot(snapshot)); err != nil {
		attempt.State = "failed"
		attempt.Error = err.Error()
		return attempt
	}
	return attempt
}

func (r *Runtime) applyAndDeliver(parent, ctx context.Context, groupID int64, attempt Attempt, snapshot ctxsnapshot.ContextSnapshot, stale func() bool) Attempt {
	if stale() || ctx.Err() != nil {
		attempt.State = "stale"
		attempt.Retrieval.StaleReason = "new_interaction"
		if ctx.Err() != nil {
			attempt.Retrieval.StaleReason = "cancelled"
		}
		return attempt
	}
	if len(attempt.Reply.Memory) > 0 {
		explicit := false
		for _, event := range snapshot.Batch {
			explicit = explicit || memory.Explicit(event.Text)
		}
		if r.Memory == nil || !explicit {
			attempt.State = "failed"
			attempt.Error = "memory changes require an explicit request"
			return attempt
		}
		// Memory is validated and staged here, but only the transaction that
		// confirms the platform action commits it.
		prepared, err := r.Memory.Prepare(ctx, groupID, snapshot.Batch, attempt.Reply.Memory)
		if err != nil {
			attempt.State = "failed"
			attempt.Error = "memory update failed: " + err.Error()
			return attempt
		}
		if err := r.Store.SavePendingMemory(ctx, attempt.ID, groupID, memory.NoteSnapshots(prepared)); err != nil {
			attempt.State = "failed"
			attempt.Error = err.Error()
			return attempt
		}
	}
	if attempt.Reply.Action == "silent" {
		attempt.State = "silent"
		return attempt
	}

	// Re-check the budget before sending: another turn may have consumed the
	// window while the model was generating. This only limits sending.
	count, lastSent, err := r.Store.RepliesSince(ctx, groupID, time.Now().Add(-time.Minute))
	if err != nil {
		attempt.State = "failed"
		attempt.Error = err.Error()
		return attempt
	}
	directed := false
	for _, event := range snapshot.Batch {
		directed = directed || event.Directed
	}
	directed = directed || snapshot.Conversation.Continuing
	if count >= r.Config.MaxPerMinute || (!directed && !lastSent.IsZero() && time.Since(lastSent) < r.Config.SpontaneousInterval) {
		attempt.State = "limited"
		return attempt
	}

	path := ""
	if attempt.Reply.Action == "sticker" {
		if r.Memes == nil {
			attempt.State = "failed"
			attempt.Error = "sticker service unavailable"
			return attempt
		}
		asset, err := r.Memes.Resolve(ctx, attempt.Reply.StickerID)
		if err != nil {
			attempt.State = "failed"
			attempt.Error = err.Error()
			return attempt
		}
		path = asset.Path
	}
	if r.Sender == nil {
		attempt.State = "preview"
		return attempt
	}
	if stale() || ctx.Err() != nil {
		attempt.State = "stale"
		attempt.Retrieval.StaleReason = "new_interaction"
		return attempt
	}
	// Pokes are rate limited per target before the sending transition; a
	// limited poke ends the turn like any other policy rejection.
	pokeUser := int64(0)
	if attempt.Reply.Action == "poke" {
		user, err := pokeTarget(attempt.Reply, snapshot.History, snapshot.Batch)
		if err != nil {
			attempt.State = "failed"
			attempt.Error = err.Error()
			return attempt
		}
		if !r.pokeAllowed(groupID, user) {
			attempt.State = "limited"
			return attempt
		}
		pokeUser = user
	}
	attempt.State = "sending"
	if err := r.Store.UpdateAttempt(ctx, attempt); err != nil {
		attempt.State = "failed"
		attempt.Error = err.Error()
		return attempt
	}

	var receipt qq.Receipt
	switch attempt.Reply.Action {
	case "reaction":
		err = r.Sender.React(ctx, groupID, attempt.Reply.Quote, attempt.Reply.EmojiID)
	case "poke":
		err = r.Sender.Poke(ctx, groupID, pokeUser)
	default:
		if r.Delivery != nil {
			receipt, err = r.Delivery.Deliver(ctx, delivery.Request{GroupID: groupID, Text: attempt.Reply.Text, Quote: attempt.Reply.Quote, Path: path})
		} else {
			receipt, err = r.Sender.Send(ctx, groupID, attempt.Reply.Text, attempt.Reply.Quote, path)
		}
		if err == nil && receipt.MessageID != "" {
			// The platform receipt is persisted before local archiving so the
			// reconciler can still identify the message if archiving fails.
			if saveErr := r.Store.SaveDelivery(ctx, attempt.ID, receipt.MessageID, "message"); saveErr != nil {
				slog.Warn("save delivery receipt", "attempt", attempt.ID, "error", saveErr)
			}
		}
	}
	finish, cancelFinish := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancelFinish()
	if err != nil {
		attempt.State = "unknown"
		var sendError *qq.SendError
		if errors.As(err, &sendError) && sendError.Definite {
			attempt.State = "failed"
		}
		attempt.Error = err.Error()
		if updateErr := r.Store.UpdateAttempt(finish, attempt); updateErr != nil {
			slog.Error("record send failure", "attempt", attempt.ID, "error", updateErr)
		}
		return attempt
	}

	attempt.State = "confirmed"
	if attempt.Reply.Action == "reaction" || attempt.Reply.Action == "poke" {
		// Reactions and pokes are visible platform actions, not messages:
		// confirm the attempt with the action fact, topic projection and
		// consultations instead of archiving an outbound event.
		record := r.ActionRecordFor(attempt.Reply, groupID)
		if err := r.Store.ConfirmAction(finish, attempt, record, snapshotNoteIDs(snapshot.Notes)); err != nil {
			attempt.State = "unknown"
			attempt.Error = "action confirmed but archive failed"
			recordCtx, cancelRecord := context.WithTimeout(context.WithoutCancel(parent), 3*time.Second)
			if updateErr := r.Store.UpdateAttempt(recordCtx, attempt); updateErr != nil {
				slog.Error("record archive failure", "attempt", attempt.ID, "error", updateErr)
			}
			// The platform action already happened; only the local archive is
			// missing, so the reconciler retries it without another send.
			if markErr := r.Store.MarkReconcile(recordCtx, attempt.ID, err.Error()); markErr != nil {
				slog.Error("mark action reconcile", "attempt", attempt.ID, "error", markErr)
			}
			cancelRecord()
			slog.Error(attempt.Error, "attempt", attempt.ID, "error", err)
			return attempt
		}
		// Staged memory committed with the action; the tracker learns the
		// participation so continuation includes it.
		r.topics.markBotAction(groupID, record.TopicID, record.ConfirmedAt)
		return attempt
	}
	text := attempt.Reply.Text
	if attempt.Reply.Action == "sticker" {
		text = "[表情包] "
		for _, asset := range snapshot.StickerAssets {
			if asset.ID == attempt.Reply.StickerID {
				text += asset.Description
			}
		}
	}
	hash := sha256.Sum256([]byte(attempt.ID))
	out := qq.Event{ID: fmt.Sprintf("out:%x", hash[:12]), MessageID: receipt.MessageID, GroupID: groupID, UserID: r.SelfID, SelfID: r.SelfID, Name: r.Persona.Name, Text: text, ReplyTo: attempt.Reply.Quote, Outbound: true, At: time.Now()}
	if err := r.Store.FinishAttempt(finish, attempt, out, snapshotNoteIDs(snapshot.Notes)); err != nil {
		attempt.State = "unknown"
		attempt.Error = "delivery confirmed but archive failed"
		record, cancelRecord := context.WithTimeout(context.WithoutCancel(parent), 3*time.Second)
		if updateErr := r.Store.UpdateAttempt(record, attempt); updateErr != nil {
			slog.Error("record archive failure", "attempt", attempt.ID, "error", updateErr)
		}
		cancelRecord()
		slog.Error(attempt.Error, "attempt", attempt.ID, "error", err)
		return attempt
	}
	r.topics.markBotReply(groupID, attempt.Reply.Plan.TopicID, out)
	return attempt
}

// ActionRecordFor rebuilds the projection record of a reaction or poke reply.
// A reaction binds to the topic of its quoted message, a poke to the topic of
// its target event; no determinable topic confirms the action alone. The
// reconciler reuses it to retry a failed local archive.
func (r *Runtime) ActionRecordFor(reply Reply, groupID int64) ActionRecord {
	record := ActionRecord{GroupID: groupID, Kind: reply.Action, ConfirmedAt: time.Now()}
	if reply.Action == "reaction" {
		record.TargetMessageID = reply.Quote
		record.TopicID = r.topics.topicIDForMessage(groupID, reply.Quote)
	} else if reply.Plan != nil {
		record.TargetEventID = reply.Plan.TargetEventID
		record.TopicID = r.topics.topicIDForEvent(groupID, reply.Plan.TargetEventID)
	}
	return record
}

// ApplyReconciledMessage finishes the in-memory projections after the
// reconciler archives an outbound message.
func (r *Runtime) ApplyReconciledMessage(e qq.Event, topicID string) {
	r.topics.markBotReply(e.GroupID, topicID, e)
}

// ApplyReconciledAction finishes the in-memory topic projection after the
// reconciler confirms an action archive.
func (r *Runtime) ApplyReconciledAction(record ActionRecord) {
	r.topics.markBotAction(record.GroupID, record.TopicID, record.ConfirmedAt)
}

// pokeAllowed reports whether the per-target poke cooldown has elapsed,
// consuming the slot either way: a failed poke still blocks the next attempt,
// so repeated failures cannot probe the target.
// ponytail: the cooldown map never shrinks; entries cost a few bytes per
// (group, user) ever poked, bounded by the group whitelist — prune expired
// entries if a long-lived instance ever makes that measurable.
func (r *Runtime) pokeAllowed(groupID, userID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pokes == nil {
		r.pokes = map[int64]map[int64]time.Time{}
	}
	if last, ok := r.pokes[groupID][userID]; ok && r.Config.PokeCooldown > 0 && time.Since(last) < r.Config.PokeCooldown {
		return false
	}
	if r.pokes[groupID] == nil {
		r.pokes[groupID] = map[int64]time.Time{}
	}
	r.pokes[groupID][userID] = time.Now()
	return true
}

// snapshotNoteIDs freezes the recalled note identities for the usage ledger.
// A confirmed send books each unique note once, independent of cache state.
func snapshotNoteIDs(notes []memory.Note) []string {
	ids := make([]string, 0, len(notes))
	seen := map[string]bool{}
	for _, note := range notes {
		if note.ID == "" || seen[note.ID] {
			continue
		}
		seen[note.ID] = true
		ids = append(ids, note.ID)
	}
	return ids
}

func observationFromSnapshot(snapshot ctxsnapshot.ContextSnapshot) topicObservation {
	assignments := map[string][]string{}
	for _, events := range [][]ConversationEvent{snapshot.Conversation.History, snapshot.Conversation.Batch} {
		for _, event := range events {
			if _, exists := assignments[event.EventID]; !exists {
				assignments[event.EventID] = append([]string(nil), event.TopicIDs...)
			}
		}
	}
	return topicObservation{Assignments: assignments}
}

func queryText(events []qq.Event) string {
	var builder strings.Builder
	for _, event := range events {
		if !event.Outbound && !event.Recalled {
			builder.WriteString(event.Text)
			builder.WriteByte('\n')
		}
	}
	runes := []rune(builder.String())
	if len(runes) > 1200 {
		runes = runes[len(runes)-1200:]
	}
	return string(runes)
}
