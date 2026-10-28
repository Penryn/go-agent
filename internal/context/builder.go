package context

import (
	"context"
	"strings"

	"github.com/phlin/go-agent/internal/conversation"
	"github.com/phlin/go-agent/internal/meme"
	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/qq"
)

type ParticipantProfile struct {
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
}

type ContextSnapshot struct {
	GroupID        int64
	Version        uint64
	History        []qq.Event
	Batch          []qq.Event
	Topics         []conversation.TopicView
	Participants   []ParticipantProfile
	Notes          []memory.Note
	Episodes       []memory.Note
	StickerAssets  []meme.Asset
	Images         []string
	Query          string
	QueryEmbedding []float32
}

type Builder interface {
	Build(context.Context, int64, uint64, []qq.Event) (ContextSnapshot, error)
}

// SnapshotBuilder is the pure assembly implementation. Fetching history and
// recall is injected as functions so the resulting snapshot is detached from
// mutable actor and storage state.
type SnapshotBuilder struct {
	History      func(context.Context, int64, int) ([]qq.Event, error)
	HistoryLimit int
	Topics       func(context.Context, int64, uint64, []qq.Event) ([]conversation.TopicView, error)
	Notes        func(context.Context, int64, string) ([]memory.Note, error)
	Stickers     func(context.Context, int64, string) ([]meme.Asset, error)
	Embed        func(context.Context, string) ([]float32, error)
}

func (b SnapshotBuilder) Build(ctx context.Context, groupID int64, version uint64, batch []qq.Event) (ContextSnapshot, error) {
	if b.HistoryLimit <= 0 {
		b.HistoryLimit = 24
	}
	history := []qq.Event{}
	var err error
	if b.History != nil {
		history, err = b.History(ctx, groupID, b.HistoryLimit)
		if err != nil {
			return ContextSnapshot{}, err
		}
	}
	topics := []conversation.TopicView{}
	if b.Topics != nil {
		topics, err = b.Topics(ctx, groupID, version, append([]qq.Event(nil), batch...))
		if err != nil {
			return ContextSnapshot{}, err
		}
	}
	query := queryText(batch)
	notes := []memory.Note{}
	if b.Notes != nil {
		notes, err = b.Notes(ctx, groupID, query)
		if err != nil {
			return ContextSnapshot{}, err
		}
	}
	assets := []meme.Asset{}
	if b.Stickers != nil {
		assets, err = b.Stickers(ctx, groupID, query)
		if err != nil {
			return ContextSnapshot{}, err
		}
	}
	var embedding []float32
	if b.Embed != nil && strings.TrimSpace(query) != "" {
		embedding, err = b.Embed(ctx, query)
		if err != nil {
			return ContextSnapshot{}, err
		}
	}
	return ContextSnapshot{GroupID: groupID, Version: version, History: cloneEvents(history), Batch: cloneEvents(batch), Topics: cloneTopics(topics), Notes: cloneNotes(notes), StickerAssets: append([]meme.Asset(nil), assets...), Query: query, QueryEmbedding: append([]float32(nil), embedding...)}, nil
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
func cloneEvents(in []qq.Event) []qq.Event {
	out := append([]qq.Event(nil), in...)
	for i := range out {
		out[i].Images = append([]qq.Attachment(nil), out[i].Images...)
	}
	return out
}
func cloneTopics(in []conversation.TopicView) []conversation.TopicView {
	out := append([]conversation.TopicView(nil), in...)
	for i := range out {
		out[i].Participants = append([]int64(nil), out[i].Participants...)
	}
	return out
}
func cloneNotes(in []memory.Note) []memory.Note {
	out := append([]memory.Note(nil), in...)
	for i := range out {
		out[i].Evidence = append([]memory.Evidence(nil), out[i].Evidence...)
	}
	return out
}
