package chat

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/phlin/go-agent/internal/cache"
	ctxsnapshot "github.com/phlin/go-agent/internal/context"
	"github.com/phlin/go-agent/internal/conversation"
	"github.com/phlin/go-agent/internal/meme"
	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/model"
	"github.com/phlin/go-agent/internal/qq"
)

// imageMediaVersion names the media processing pipeline; bump it when image
// normalization changes so cached data URLs cannot be reused.
const imageMediaVersion = "v1"

// runtimeAdmissionBuilder performs only local work: the history tail, the topic
// graph and relation hints. It never calls models, wide storage reads or media.
type runtimeAdmissionBuilder struct{ runtime *Runtime }

func (b runtimeAdmissionBuilder) Build(ctx context.Context, groupID int64, version uint64, batch []qq.Event) (ctxsnapshot.AdmissionSnapshot, error) {
	r := b.runtime
	live := make([]qq.Event, 0, len(batch))
	for _, event := range batch {
		if !event.Recalled {
			live = append(live, event)
		}
	}
	snapshot := ctxsnapshot.AdmissionSnapshot{GroupID: groupID, ActorVersion: version, Batch: live}
	if len(live) == 0 {
		return snapshot, nil
	}
	history, err := r.Store.Recent(ctx, groupID, r.Config.History)
	if err != nil {
		return ctxsnapshot.AdmissionSnapshot{}, err
	}
	history = priorConversation(history, live)
	// A bounded chronological tail keeps author and quotation identity intact.
	runes := 0
	for i := len(history) - 1; i >= 0; i-- {
		runes += len([]rune(history[i].Text))
		if runes > r.Config.ContextRunes {
			history = history[i+1:]
			break
		}
	}
	topics := r.topics.observe(groupID, history, live, r.Config.ActiveWindow)
	visible := combineEvents(history, live)
	snapshot.History = cloneEvents(history)
	snapshot.Conversation = buildConversationContextFromVisible(live, history, visible, topics)
	snapshot.Query = queryText(live)
	return snapshot, nil
}

// runtimeContextBuilder assembles the full, immutable turn snapshot. Recall,
// stickers, images and the query embedding share one visible conversation and
// one query vector.
type runtimeContextBuilder struct{ runtime *Runtime }

type turnResources struct {
	Notes  []memory.Note
	Assets []meme.Asset
	Images []string
	Trace  memory.Trace
}

// contextCacheValue deliberately excludes image data: images have their own
// longer-lived per-URL cache and can be large.
type contextCacheValue struct {
	Notes  []memory.Note
	Assets []meme.Asset
	Trace  memory.Trace
}

func (b runtimeContextBuilder) Build(ctx context.Context, admission ctxsnapshot.AdmissionSnapshot) (ctxsnapshot.ContextSnapshot, error) {
	r := b.runtime
	resources := turnResources{Notes: []memory.Note{}, Assets: []meme.Asset{}, Images: []string{}}
	var vector []float32
	modelVersion := ""
	if r.Embedding != nil && strings.TrimSpace(admission.Query) != "" {
		var err error
		vector, err = r.embedQuery(ctx, admission.GroupID, admission.AttemptID, admission.Query)
		if err != nil {
			resources.Trace.Degraded = append(resources.Trace.Degraded, "embedding")
		} else {
			modelVersion = r.Embedding.EmbeddingVersion()
		}
	}

	key := r.contextCacheKey(ctx, admission.GroupID, admission.ActorVersion)
	cached := false
	embeddingDegraded := slices.Contains(resources.Trace.Degraded, "embedding")
	if key != "" {
		var stored contextCacheValue
		if r.Cache.Get(key, &stored) {
			stored.Trace.RecallCacheHit = true
			stored.Trace.ImageCacheHit = false
			resources.Notes = stored.Notes
			resources.Assets = stored.Assets
			resources.Trace = stored.Trace
			if embeddingDegraded {
				resources.Trace.Degraded = append(resources.Trace.Degraded, "embedding")
			}
			cached = true
		}
	}
	if !cached {
		resources = r.retrieveResources(ctx, admission, vector, modelVersion, resources)
		if key != "" && !slices.Contains(resources.Trace.Degraded, "memory") && !slices.Contains(resources.Trace.Degraded, "stickers") {
			r.Cache.Set(key, contextCacheValue{Notes: resources.Notes, Assets: resources.Assets, Trace: resources.Trace}, r.Config.Cache.Context)
		}
	}
	images, imageHit, imageFailed := r.fetchImages(ctx, admission.Batch)
	resources.Images = images
	resources.Trace.ImageCacheHit = imageHit
	if imageFailed {
		resources.Trace.Degraded = append(resources.Trace.Degraded, "images")
	}

	return ctxsnapshot.ContextSnapshot{
		GroupID:        admission.GroupID,
		Version:        admission.ActorVersion,
		History:        ctxsnapshot.LimitEvents(admission.History, ctxsnapshot.MaxHistory),
		Batch:          ctxsnapshot.LimitEvents(admission.Batch, ctxsnapshot.MaxBatch),
		Conversation:   ctxsnapshot.LimitConversation(admission.Conversation, ctxsnapshot.MaxTopics),
		Topics:         topicViews(admission.Conversation.Topics),
		Notes:          ctxsnapshot.CloneNotes(resources.Notes),
		StickerAssets:  ctxsnapshot.CloneAssets(resources.Assets),
		Images:         append([]string(nil), resources.Images...),
		Query:          admission.Query,
		QueryEmbedding: append([]float32(nil), vector...),
		Retrieval:      resources.Trace,
	}, nil
}

func (r *Runtime) retrieveResources(ctx context.Context, admission ctxsnapshot.AdmissionSnapshot, vector []float32, modelVersion string, resources turnResources) turnResources {
	if r.Memory != nil {
		notes, trace, err := r.Memory.RecallCached(ctx, memory.Query{
			GroupID:      admission.GroupID,
			Events:       admission.Batch,
			Text:         admission.Query,
			Vector:       vector,
			ModelVersion: modelVersion,
			TopicIDs:     visibleTopicIDs(admission.Conversation),
		}, admission.ActorVersion, r.Config.Cache.Recall)
		if err != nil {
			resources.Trace.Degraded = append(resources.Trace.Degraded, "memory")
		} else {
			resources.Notes = notes
			resources.Trace.IndexCacheHit = trace.IndexCacheHit
			resources.Trace.RecallCacheHit = resources.Trace.RecallCacheHit || trace.RecallCacheHit
			resources.Trace.Semantic = trace.Semantic
			resources.Trace.Lexical = trace.Lexical
			resources.Trace.Contextual = trace.Contextual
			resources.Trace.Selected = trace.Selected
			resources.Trace.Degraded = append(resources.Trace.Degraded, trace.Degraded...)
			r.Memory.RecordUsage(notes)
		}
	}
	if r.Memes != nil {
		assets, err := r.Memes.CandidatesCached(ctx, admission.GroupID, admission.Query, vector, modelVersion, time.Now().Unix()/max(1, int64(r.Memes.Config.Cooldown.Seconds())), r.Config.Cache.Sticker)
		if err != nil {
			resources.Trace.Degraded = append(resources.Trace.Degraded, "stickers")
		} else {
			resources.Assets = assets
		}
	}
	return resources
}

func (r *Runtime) embedQuery(ctx context.Context, groupID int64, attemptID, query string) ([]float32, error) {
	call, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if gateway, ok := r.Embedding.(model.ModelGateway); ok {
		vector, err := gateway.EmbedRequest(call, model.EmbedRequest{Text: query, ModelVersion: r.Embedding.EmbeddingVersion(), Priority: model.PriorityInteractive, GroupID: groupID, AttemptID: attemptID})
		if err != nil {
			return nil, err
		}
		return append([]float32(nil), vector...), nil
	}
	vector, err := r.Embedding.Embed(call, query)
	if err != nil {
		return nil, err
	}
	return append([]float32(nil), vector...), nil
}

func (r *Runtime) fetchImages(ctx context.Context, batch []qq.Event) ([]string, bool, bool) {
	images := []string{}
	hit := false
	failed := false
	for _, event := range batch {
		for _, image := range event.Images {
			if len(images) >= ctxsnapshot.MaxImages {
				return images, hit, failed
			}
			key := cache.ImageKey(image.URL, imageMediaVersion)
			var data string
			if r.Cache != nil && r.Config.Cache.Image > 0 && r.Cache.Get(key, &data) {
				hit = true
				images = append(images, data)
				continue
			}
			call, cancel := context.WithTimeout(ctx, 3*time.Second)
			data, err := r.ImageFetcher(call, image.URL)
			cancel()
			if err != nil {
				failed = true
				continue
			}
			if r.Cache != nil && r.Config.Cache.Image > 0 {
				r.Cache.Set(key, data, r.Config.Cache.Image)
			}
			images = append(images, data)
		}
	}
	return images, hit, failed
}

func (r *Runtime) contextCacheKey(ctx context.Context, groupID int64, actorVersion uint64) string {
	if r.Cache == nil || r.Config.Cache.Context <= 0 {
		return ""
	}
	store, ok := r.Store.(memory.RevisionStore)
	if !ok {
		return ""
	}
	memoryRevision, err := store.MemoryRevision(ctx, groupID)
	if err != nil {
		return ""
	}
	assetRevision := int64(0)
	if assets, ok := r.Store.(meme.RevisionStore); ok {
		if revision, err := assets.StickerRevision(ctx); err == nil {
			assetRevision = revision
		}
	}
	return cache.ContextKey(groupID, actorVersion, memoryRevision, r.topics.Revision(groupID), assetRevision)
}

func visibleTopicIDs(contextView conversation.Context) []string {
	ids := []string{}
	seen := map[string]bool{}
	for _, events := range [][]conversation.Event{contextView.Batch, contextView.History} {
		for _, event := range events {
			for _, id := range event.TopicIDs {
				if id != "" && !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
		}
	}
	return ids
}

func topicViews(topics []conversation.Topic) []conversation.TopicView {
	out := make([]conversation.TopicView, 0, len(topics))
	for _, topic := range topics {
		out = append(out, conversation.TopicView{ID: topic.ID, Summary: topic.Summary, Participants: append([]int64(nil), topic.Participants...), Status: topic.State, Revision: 1})
	}
	return out
}

func cloneEvents(in []qq.Event) []qq.Event {
	out := append([]qq.Event(nil), in...)
	for i := range out {
		out[i].Images = append([]qq.Attachment(nil), out[i].Images...)
	}
	return out
}

// fetchImageData is the production image loader. Tests inject ImageFetcher to
// count downloads without touching the network.
func fetchImageData(ctx context.Context, url string) (string, error) {
	body, mime, err := qq.FetchImage(ctx, nil, url, 8<<20)
	if err != nil {
		return "", err
	}
	return qq.ImageData(body, mime)
}
