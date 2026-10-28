package chat

import (
	"context"

	ctxsnapshot "github.com/phlin/go-agent/internal/context"
	"github.com/phlin/go-agent/internal/conversation"
	"github.com/phlin/go-agent/internal/qq"
)

// runtimeContextBuilder is the compatibility seam used while the legacy
// Runtime remains the composition root. It freezes the exact same turn data
// used by generation, so callers can migrate to context.Builder without
// changing prompt semantics.
type runtimeContextBuilder struct{ runtime *Runtime }

func (b runtimeContextBuilder) Build(ctx context.Context, groupID int64, version uint64, batch []qq.Event) (ctxsnapshot.ContextSnapshot, error) {
	turn, err := b.runtime.prepareTurn(ctx, groupID, batch)
	if err != nil {
		return ctxsnapshot.ContextSnapshot{}, err
	}
	topics := make([]conversation.TopicView, 0, len(turn.topics.Topics))
	for _, topic := range turn.topics.Topics {
		topics = append(topics, conversation.TopicView{ID: topic.ID, Participants: append([]int64(nil), topic.Participants...), Status: topic.State})
	}
	snapshot := ctxsnapshot.ContextSnapshot{GroupID: groupID, Version: version, History: append([]qq.Event(nil), turn.history...), Batch: append([]qq.Event(nil), turn.batch...), Topics: topics, Query: turn.query}
	if b.runtime.Embedding != nil && turn.query != "" {
		vector, embedErr := b.runtime.Embedding.Embed(ctx, turn.query)
		if embedErr == nil {
			snapshot.QueryEmbedding = append([]float32(nil), vector...)
		}
	}
	return snapshot, nil
}
