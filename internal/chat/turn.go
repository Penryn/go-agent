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
		"persona":              r.Persona,
		"now":                  time.Now().Format(time.RFC3339),
		"history":              snapshot.History,
		"batch":                snapshot.Batch,
		"conversation_context": snapshot.Conversation,
		"notes":                snapshot.Notes,
		"sticker_candidates":   snapshot.StickerAssets,
		"loaded_image_count":   len(snapshot.Images),
	}
	var result model.Result
	var err error
	if gateway, ok := r.Model.(model.ModelGateway); ok {
		result, err = gateway.GenerateRequest(ctx, model.GenerateRequest{PromptVersion: "chat-v1", System: Prompt, Input: input, Images: snapshot.Images, Priority: model.PriorityInteractive, GroupID: attempt.GroupID, AttemptID: attempt.ID})
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
		if err := r.Memory.Apply(ctx, groupID, snapshot.Batch, attempt.Reply.Memory, nil); err != nil {
			attempt.State = "failed"
			attempt.Error = "memory update failed: " + err.Error()
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
	attempt.State = "sending"
	if err := r.Store.UpdateAttempt(ctx, attempt); err != nil {
		attempt.State = "failed"
		attempt.Error = err.Error()
		return attempt
	}

	var receipt qq.Receipt
	if r.Delivery != nil {
		receipt, err = r.Delivery.Deliver(ctx, delivery.Request{GroupID: groupID, Text: attempt.Reply.Text, Quote: attempt.Reply.Quote, Path: path})
	} else if r.Sender != nil {
		receipt, err = r.Sender.Send(ctx, groupID, attempt.Reply.Text, attempt.Reply.Quote, path)
	} else {
		err = errors.New("delivery sender unavailable")
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
	if err := r.Store.FinishAttempt(finish, attempt, out); err != nil {
		attempt.State = "unknown"
		attempt.Error = "delivery confirmed but archive failed"
		slog.Error(attempt.Error, "attempt", attempt.ID, "error", err)
		return attempt
	}
	r.topics.markBotReply(groupID, attempt.Reply.Plan.TopicID, out)
	return attempt
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
