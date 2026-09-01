package meme

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
	retrievalsvc "github.com/phlin/go-agent/internal/services/retrieval"
)

func TestObserveEventAndBuildSendSegments(t *testing.T) {
	store := inmemory.NewStore()
	service := New(store, config.MemeConfig{AutoCollect: true}, WithRetriever(retrievalsvc.New(store, store, nil, nil, retrievalsvc.Config{})))

	err := service.ObserveEvent(context.Background(), conversationdomain.ConversationEvent{
		EventID: "e1",
		GroupID: 1,
		Attachments: []mediadomain.MultimodalAttachment{
			{
				AttachmentID: "a1",
				Kind:         mediadomain.MediaSticker,
				ObjectKey:    "memes/a1.webp",
				ContentHash:  "hash-a1",
			},
		},
	}, []mediadomain.MediaDescriptor{{
		AttachmentID: "a1", Kind: mediadomain.MediaSticker, Summary: "测试表情", Confidence: 1,
	}})
	if err != nil {
		t.Fatalf("observe event: %v", err)
	}

	results, err := service.Search(context.Background(), ports.MemeQuery{GroupID: 1, Query: "测试", TopK: 3})
	if err != nil {
		t.Fatalf("search meme: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected search results")
	}

	segments, err := service.BuildSendSegments(context.Background(), "meme-hash-a1", "reply-1", "收下这张")
	if err != nil {
		t.Fatalf("build send segments: %v", err)
	}
	if len(segments) < 2 || segments[0].Type != "reply" || segments[1].Type != "image" {
		t.Fatalf("unexpected segments: %#v", segments)
	}
}

func TestDudFeedbackSinksColdMemes(t *testing.T) {
	store := inmemory.NewStore()
	ctx := context.Background()
	svc := New(store, config.MemeConfig{AutoCollect: true, RepeatCooldown: "10m"}, WithRetriever(retrievalsvc.New(store, store, nil, nil, retrievalsvc.Config{})))

	// 收两张表情
	for i, text := range []string{"好图", "烂图"} {
		event := conversationdomain.ConversationEvent{
			EventID: fmt.Sprintf("ev-%d", i), GroupID: 1, UserID: 7, Text: text,
			Attachments: []mediadomain.MultimodalAttachment{{AttachmentID: fmt.Sprintf("a-%d", i), Kind: mediadomain.MediaImage, ObjectKey: fmt.Sprintf("k%d.jpg", i)}},
		}
		if err := svc.ObserveEvent(ctx, event, []mediadomain.MediaDescriptor{{
			AttachmentID: fmt.Sprintf("a-%d", i), Kind: mediadomain.MediaImage,
			Summary: text, MemeSignals: []string{"reaction"}, Confidence: 1,
		}}); err != nil {
			t.Fatalf("observe: %v", err)
		}
	}

	// 两张各发三次
	all := []string{}
	for id := range memeIDs(t, store) {
		all = append(all, id)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 memes, got %v", all)
	}
	for _, id := range all {
		for range 3 {
			if err := store.MarkMemeSent(ctx, id); err != nil {
				t.Fatalf("mark sent: %v", err)
			}
		}
	}

	// 第一张反复哑弹：发送 -> 冷场超窗 -> 检索时兜底结算
	dud := all[0]
	svc.markSentAt(dud)
	svc.sweepExpiredDuds(ctx, time.Now().Add(dudObservationWindow+time.Minute))
	asset, _, err := store.GetMeme(ctx, dud)
	if err != nil {
		t.Fatalf("get dud meme: %v", err)
	}
	if asset.DudCount != 1 || asset.SendCount != 3 {
		t.Fatalf("expected dud=1 send=3, got dud=%d send=%d", asset.DudCount, asset.SendCount)
	}

	// 哑弹率高的应排在后面（空 Query 匹配全部）
	results, err := svc.Search(ctx, ports.MemeQuery{GroupID: 1, Query: "", TopK: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %+v", results)
	}
	if results[len(results)-1].MemeID != dud {
		t.Fatalf("dud meme %s should rank last, got order %+v", dud, results)
	}
}

func TestCollectsOnlySafeConfidentMemeCandidates(t *testing.T) {
	store := inmemory.NewStore()
	svc := New(store, config.MemeConfig{AutoCollect: true, CandidateThreshold: 0.6}, WithRetriever(retrievalsvc.New(store, store, nil, nil, retrievalsvc.Config{})))
	event := conversationdomain.ConversationEvent{EventID: "e1", GroupID: 1, Attachments: []mediadomain.MultimodalAttachment{
		{AttachmentID: "safe", Kind: mediadomain.MediaImage, ObjectKey: "safe.jpg"},
		{AttachmentID: "unsafe", Kind: mediadomain.MediaSticker, ObjectKey: "unsafe.webp"},
		{AttachmentID: "unclear", Kind: mediadomain.MediaSticker, ObjectKey: "unclear.webp"},
	}}
	descriptors := []mediadomain.MediaDescriptor{
		{AttachmentID: "safe", Kind: mediadomain.MediaImage, MemeKeywords: []string{"doge"}, Confidence: 0.9},
		{AttachmentID: "unsafe", Kind: mediadomain.MediaSticker, SafetySignals: []string{"violence"}, Confidence: 0.9},
		{AttachmentID: "unclear", Kind: mediadomain.MediaSticker, Confidence: 0.2},
	}
	if err := svc.ObserveEvent(context.Background(), event, descriptors); err != nil {
		t.Fatalf("observe: %v", err)
	}
	results, err := svc.Search(context.Background(), ports.MemeQuery{GroupID: 1, Query: "doge", TopK: 5})
	if err != nil || len(results) != 1 {
		t.Fatalf("expected one safe meme, results=%+v err=%v", results, err)
	}
}

func TestSearchAppliesTagsDefaultsScopeAndStrictCooldown(t *testing.T) {
	store := inmemory.NewStore()
	seed := func(id string, groupID int64, emotion string) {
		t.Helper()
		if err := store.UpsertMeme(context.Background(), mediadomain.MemeAsset{
			MemeID: id, GroupID: groupID, ObjectKey: id + ".webp", Status: "approved", CreatedAt: time.Now(),
		}, mediadomain.MemeDescriptor{
			MemeID: id, Summary: "reaction", EmotionTags: []string{emotion}, SceneTags: []string{"chat"}, Confidence: 1,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("global", 0, "happy")
	seed("local", 1, "happy")
	seed("wrong-emotion", 1, "sad")
	svc := New(store, config.MemeConfig{SearchTopK: 2, RepeatCooldown: "10m", PreferGroupScoped: true}, WithRetriever(retrievalsvc.New(store, store, nil, nil, retrievalsvc.Config{})))

	results, err := svc.Search(context.Background(), ports.MemeQuery{GroupID: 1, Emotion: "happy", Scene: "chat"})
	if err != nil || len(results) != 2 || results[0].MemeID != "local" {
		t.Fatalf("unexpected filtered ranking: results=%+v err=%v", results, err)
	}
	if err := store.MarkMemeSent(context.Background(), "local"); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	results, err = svc.Search(context.Background(), ports.MemeQuery{GroupID: 1, Emotion: "happy", Scene: "chat", ExcludeRecent: true})
	if err != nil || len(results) != 1 || results[0].MemeID != "global" {
		t.Fatalf("strict cooldown failed: results=%+v err=%v", results, err)
	}
}

func TestBuildSendSegmentsRejectsUnapprovedMeme(t *testing.T) {
	store := inmemory.NewStore()
	if err := store.UpsertMeme(context.Background(), mediadomain.MemeAsset{
		MemeID: "pending", GroupID: 1, ObjectKey: "pending.webp", Status: "pending",
	}, mediadomain.MemeDescriptor{MemeID: "pending"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := New(store, config.MemeConfig{}).BuildSendSegments(context.Background(), "pending", "", ""); err == nil {
		t.Fatal("unapproved meme should not be sent")
	}
}

type vectorIndexer struct{ calls int }

func (v *vectorIndexer) IndexMeme(_ context.Context, _ string, _ string, _ int64) error {
	v.calls++
	return nil
}

func (v *vectorIndexer) SearchMemes(context.Context, int64, string, int, float64) ([]mediadomain.MemeSearchResult, error) {
	return nil, nil
}

func (v *vectorIndexer) DeleteMeme(context.Context, string) error { return nil }

func TestObserveEventUsesOutboxForVectorIndex(t *testing.T) {
	store := inmemory.NewStore()
	indexer := &vectorIndexer{}
	outbox := &recordingMemeOutbox{}
	svc := New(store, config.MemeConfig{AutoCollect: true, CandidateThreshold: 0.6}, WithVectorStore(indexer), WithOutbox(outbox))
	if err := svc.ObserveEvent(context.Background(), conversationdomain.ConversationEvent{
		EventID: "e-outbox", GroupID: 1,
		Attachments: []mediadomain.MultimodalAttachment{{AttachmentID: "a", Kind: mediadomain.MediaSticker, ObjectKey: "a.webp"}},
	}, []mediadomain.MediaDescriptor{{AttachmentID: "a", Kind: mediadomain.MediaSticker, Confidence: 1, MemeSignals: []string{"reaction"}}}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if outbox.kind != "meme_vector_index" || indexer.calls != 0 {
		t.Fatalf("vector index was not durable: outbox=%+v calls=%d", outbox, indexer.calls)
	}
}

type recordingMemeOutbox struct {
	kind, key string
	body      []byte
}

type fallbackMemeStore struct {
	*inmemory.Store
	calls int
}

func (s *fallbackMemeStore) SearchMemes(ctx context.Context, query ports.MemeQuery) ([]mediadomain.MemeSearchResult, error) {
	s.calls++
	if s.calls == 1 {
		return nil, nil
	}
	return s.Store.SearchMemes(ctx, query)
}

type missingVectorMeme struct{}

func (missingVectorMeme) IndexMeme(context.Context, string, string, int64) error { return nil }
func (missingVectorMeme) SearchMemes(context.Context, int64, string, int, float64) ([]mediadomain.MemeSearchResult, error) {
	return []mediadomain.MemeSearchResult{{MemeID: "missing-vector-result"}}, nil
}
func (missingVectorMeme) DeleteMeme(context.Context, string) error { return nil }

func TestSearchFallsBackToBM25AfterHybridCandidatesAreFiltered(t *testing.T) {
	base := inmemory.NewStore()
	if err := base.UpsertMeme(context.Background(), mediadomain.MemeAsset{
		MemeID: "keyword-hit", GroupID: 1, ObjectKey: "keyword.webp", Status: "approved",
	}, mediadomain.MemeDescriptor{MemeID: "keyword-hit", Summary: "可用关键词结果", Keywords: []string{"关键词"}}); err != nil {
		t.Fatal(err)
	}
	store := &fallbackMemeStore{Store: base}
	retriever := retrievalsvc.New(store, store, nil, missingVectorMeme{}, retrievalsvc.Config{MemeCandidateK: 1})
	svc := New(store, config.MemeConfig{SearchTopK: 1}, WithRetriever(retriever))
	results, err := svc.Search(context.Background(), ports.MemeQuery{GroupID: 1, Query: "关键词", TopK: 1})
	if err != nil || len(results) != 1 || results[0].MemeID != "keyword-hit" || store.calls < 2 {
		t.Fatalf("expected BM25 fallback, results=%+v calls=%d err=%v", results, store.calls, err)
	}
}

func (o *recordingMemeOutbox) Enqueue(_ context.Context, kind, key string, body []byte) error {
	o.kind, o.key, o.body = kind, key, body
	return nil
}

// memeIds 辅助：inmemory 没有列表接口，从检索拿全部。
func memeIDs(t *testing.T, store *inmemory.Store) map[string]struct{} {
	t.Helper()
	ids := map[string]struct{}{}
	results, err := store.SearchMemes(context.Background(), ports.MemeQuery{GroupID: 1, Query: "", TopK: 10})
	if err != nil {
		t.Fatalf("search all: %v", err)
	}
	for _, r := range results {
		ids[r.MemeID] = struct{}{}
	}
	return ids
}
