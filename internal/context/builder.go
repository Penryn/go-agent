package context

import (
	"context"

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
