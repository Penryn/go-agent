package chat

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/phlin/go-agent/internal/meme"
	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/model"
	"github.com/phlin/go-agent/internal/qq"
)

// turnContext contains the immutable conversation material for one model turn.
// It is materialized once so retrieval, generation, validation, and delivery
// all observe the same batch, history, topics, and query text.
type turnContext struct {
	batch        []qq.Event
	history      []qq.Event
	visible      []qq.Event
	topics       topicObservation
	conversation ConversationContext
	query        string
}

type turnResources struct {
	notes  []memory.Note
	assets []meme.Asset
	images []string
}

func (r *Runtime) prepareTurn(ctx context.Context, groupID int64, source []qq.Event) (turnContext, error) {
	batch := make([]qq.Event, 0, len(source))
	for _, event := range source {
		if !event.Recalled {
			batch = append(batch, event)
		}
	}
	if len(batch) == 0 {
		return turnContext{batch: batch}, nil
	}

	history, err := r.Store.Recent(ctx, groupID, r.Config.History)
	if err != nil {
		return turnContext{}, err
	}
	history = priorConversation(history, batch)
	// A bounded chronological tail keeps author and quotation identity intact.
	runes := 0
	for i := len(history) - 1; i >= 0; i-- {
		runes += len([]rune(history[i].Text))
		if runes > r.Config.ContextRunes {
			history = history[i+1:]
			break
		}
	}

	topics := r.topics.observe(groupID, history, batch, r.Config.ActiveWindow)
	visible := combineEvents(history, batch)
	return turnContext{
		batch:        batch,
		history:      history,
		visible:      visible,
		topics:       topics,
		conversation: buildConversationContextFromVisible(batch, history, visible, topics),
		query:        queryText(batch),
	}, nil
}

func (r *Runtime) retrieveTurn(ctx context.Context, groupID int64, turn turnContext, attempt *Attempt) turnResources {
	resources := turnResources{notes: []memory.Note{}, assets: []meme.Asset{}, images: []string{}}
	if r.Memory != nil {
		var err error
		resources.notes, attempt.Retrieval, err = r.Memory.Recall(ctx, groupID, turn.batch, turn.query)
		if err != nil {
			attempt.Retrieval.Degraded = append(attempt.Retrieval.Degraded, "memory")
		}
	}
	if r.Memes != nil {
		var err error
		resources.assets, err = r.Memes.Candidates(ctx, groupID, turn.query)
		if err != nil {
			attempt.Retrieval.Degraded = append(attempt.Retrieval.Degraded, "stickers")
		}
	}
	for _, event := range turn.batch {
		if len(resources.images) >= 2 {
			break
		}
		for _, image := range event.Images {
			if len(resources.images) >= 2 {
				break
			}
			call, cancel := context.WithTimeout(ctx, 3*time.Second)
			body, mime, fetchErr := qq.FetchImage(call, nil, image.URL, 8<<20)
			cancel()
			if fetchErr == nil {
				data, err := qq.ImageData(body, mime)
				if err == nil {
					resources.images = append(resources.images, data)
				}
			}
		}
	}
	return resources
}

func (r *Runtime) generateTurn(ctx context.Context, attempt Attempt, turn turnContext, resources turnResources) Attempt {
	if r.Model == nil {
		attempt.State = "silent"
		attempt.Reply.Reason = "model_unavailable"
		return attempt
	}
	result, err := r.Model.Generate(ctx, Prompt, map[string]any{
		"persona":              r.Persona,
		"now":                  time.Now().Format(time.RFC3339),
		"history":              turn.history,
		"batch":                turn.batch,
		"conversation_context": turn.conversation,
		"notes":                resources.notes,
		"sticker_candidates":   resources.assets,
		"loaded_image_count":   len(resources.images),
	}, resources.images)
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
	if err := validate(attempt.Reply, turn.visible, resources.assets, turn.topics); err != nil {
		attempt.State = "failed"
		attempt.Error = err.Error()
		return attempt
	}
	return attempt
}

func (r *Runtime) applyAndDeliver(parent, ctx context.Context, groupID int64, attempt Attempt, turn turnContext, resources turnResources, stale func() bool) Attempt {
	if stale() || ctx.Err() != nil {
		attempt.State = "stale"
		return attempt
	}
	if len(attempt.Reply.Memory) > 0 {
		explicit := false
		for _, event := range turn.batch {
			explicit = explicit || memory.Explicit(event.Text)
		}
		if r.Memory == nil || !explicit {
			attempt.State = "failed"
			attempt.Error = "memory changes require an explicit request"
			return attempt
		}
		if err := r.Memory.Apply(ctx, groupID, turn.batch, attempt.Reply.Memory, nil); err != nil {
			attempt.State = "failed"
			attempt.Error = "memory update failed: " + err.Error()
			return attempt
		}
	}
	if attempt.Reply.Action == "silent" {
		attempt.State = "silent"
		return attempt
	}

	count, lastSent, err := r.Store.RepliesSince(ctx, groupID, time.Now().Add(-time.Minute))
	if err != nil {
		attempt.State = "failed"
		attempt.Error = err.Error()
		return attempt
	}
	directed := false
	for _, event := range turn.batch {
		directed = directed || event.Directed
	}
	directed = directed || turn.conversation.Continuing
	if count >= r.Config.MaxPerMinute || (!directed && !lastSent.IsZero() && time.Since(lastSent) < r.Config.SpontaneousInterval) {
		attempt.State = "limited"
		return attempt
	}

	path := ""
	if attempt.Reply.Action == "sticker" {
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
		return attempt
	}
	attempt.State = "sending"
	if err := r.Store.UpdateAttempt(ctx, attempt); err != nil {
		attempt.State = "failed"
		attempt.Error = err.Error()
		return attempt
	}

	receipt, err := r.Sender.Send(ctx, groupID, attempt.Reply.Text, attempt.Reply.Quote, path)
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
		for _, asset := range resources.assets {
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
