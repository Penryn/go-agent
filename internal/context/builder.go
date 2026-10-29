package context

import (
	"context"
	"strings"

	"github.com/phlin/go-agent/internal/conversation"
	"github.com/phlin/go-agent/internal/meme"
	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/qq"
)

// Fixed per-turn context budgets. They bound prompt size independently of the
// inbound event buffer and retrieval caps.
const (
	MaxBatch    = 8
	MaxHistory  = 24
	MaxTopics   = 4
	MaxStickers = 5
	MaxImages   = 2
)

type ParticipantProfile struct {
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
}

// AdmissionSnapshot is the immutable result of the cheap admission step:
// history tail, current batch, topic relation hints and query text.
type AdmissionSnapshot struct {
	GroupID      int64
	ActorVersion uint64
	AttemptID    string
	History      []qq.Event
	Batch        []qq.Event
	Conversation conversation.Context
	Query        string
}

type AdmissionBuilder interface {
	Build(context.Context, int64, uint64, []qq.Event) (AdmissionSnapshot, error)
}

// ContextSnapshot is everything one admitted turn may observe. It is frozen
// before retrieval and generation so every consumer sees the same inputs.
type ContextSnapshot struct {
	GroupID        int64
	Version        uint64
	History        []qq.Event
	Batch          []qq.Event
	Conversation   conversation.Context
	Topics         []conversation.TopicView
	Participants   []ParticipantProfile
	Notes          []memory.Note
	StickerAssets  []meme.Asset
	Images         []string
	Query          string
	QueryEmbedding []float32
	Retrieval      memory.Trace
}

type Builder interface {
	Build(context.Context, AdmissionSnapshot) (ContextSnapshot, error)
}

// SnapshotBuilder is the pure assembly implementation. Fetching recall,
// stickers, images and the embedding is injected so the resulting snapshot is
// detached from mutable actor and storage state.
type SnapshotBuilder struct {
	Notes    func(context.Context, int64, string, []float32) ([]memory.Note, error)
	Stickers func(context.Context, int64, string, []float32) ([]meme.Asset, error)
	Images   func(context.Context, []qq.Event) ([]string, error)
	Embed    func(context.Context, string) ([]float32, error)
}

func (b SnapshotBuilder) Build(ctx context.Context, admission AdmissionSnapshot) (ContextSnapshot, error) {
	var embedding []float32
	var err error
	if b.Embed != nil && strings.TrimSpace(admission.Query) != "" {
		embedding, err = b.Embed(ctx, admission.Query)
		if err != nil {
			return ContextSnapshot{}, err
		}
	}
	notes := []memory.Note{}
	if b.Notes != nil {
		notes, err = b.Notes(ctx, admission.GroupID, admission.Query, embedding)
		if err != nil {
			return ContextSnapshot{}, err
		}
	}
	assets := []meme.Asset{}
	if b.Stickers != nil {
		assets, err = b.Stickers(ctx, admission.GroupID, admission.Query, embedding)
		if err != nil {
			return ContextSnapshot{}, err
		}
	}
	images := []string{}
	if b.Images != nil {
		images, err = b.Images(ctx, admission.Batch)
		if err != nil {
			return ContextSnapshot{}, err
		}
	}
	return ContextSnapshot{
		GroupID:        admission.GroupID,
		Version:        admission.ActorVersion,
		History:        LimitEvents(admission.History, MaxHistory),
		Batch:          LimitEvents(admission.Batch, MaxBatch),
		Conversation:   LimitConversation(admission.Conversation, MaxTopics),
		Topics:         cloneTopics(topicViews(admission.Conversation.Topics)),
		Notes:          CloneNotes(notes),
		StickerAssets:  CloneAssets(assets[:min(len(assets), MaxStickers)]),
		Images:         append([]string(nil), images[:min(len(images), MaxImages)]...),
		Query:          admission.Query,
		QueryEmbedding: append([]float32(nil), embedding...),
	}, nil
}

// LimitEvents keeps the most recent events and isolates the returned slice.
func LimitEvents(in []qq.Event, limit int) []qq.Event {
	if limit > 0 && len(in) > limit {
		in = in[len(in)-limit:]
	}
	out := append([]qq.Event(nil), in...)
	for i := range out {
		out[i].Images = append([]qq.Attachment(nil), out[i].Images...)
	}
	return out
}

// LimitConversation copies the relation view and applies the same budgets the
// model sees.
func LimitConversation(in conversation.Context, maxTopics int) conversation.Context {
	out := conversation.Context{
		Continuing: in.Continuing,
		Topics:     limitTopics(in.Topics, maxTopics),
		Batch:      limitEventsOf(in.Batch, MaxBatch),
		History:    limitEventsOf(in.History, MaxHistory),
	}
	return out
}

func limitTopics(in []conversation.Topic, limit int) []conversation.Topic {
	if limit > 0 && len(in) > limit {
		in = in[:limit]
	}
	out := append([]conversation.Topic(nil), in...)
	for i := range out {
		out[i].Participants = append([]int64(nil), out[i].Participants...)
		out[i].EventIDs = append([]string(nil), out[i].EventIDs...)
	}
	return out
}

func limitEventsOf(in []conversation.Event, limit int) []conversation.Event {
	if limit > 0 && len(in) > limit {
		in = in[len(in)-limit:]
	}
	out := append([]conversation.Event(nil), in...)
	for i := range out {
		out[i].TopicIDs = append([]string(nil), out[i].TopicIDs...)
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

func CloneNotes(in []memory.Note) []memory.Note {
	out := append([]memory.Note(nil), in...)
	for i := range out {
		out[i].Evidence = append([]memory.Evidence(nil), out[i].Evidence...)
	}
	return out
}

func CloneAssets(in []meme.Asset) []meme.Asset {
	out := append([]meme.Asset(nil), in...)
	for i := range out {
		out[i].Tags = append([]string(nil), out[i].Tags...)
	}
	return out
}

func topicViews(topics []conversation.Topic) []conversation.TopicView {
	out := make([]conversation.TopicView, 0, len(topics))
	for _, topic := range topics {
		out = append(out, conversation.TopicView{ID: topic.ID, Summary: topic.Summary, Participants: append([]int64(nil), topic.Participants...), Status: topic.State})
	}
	return out
}
