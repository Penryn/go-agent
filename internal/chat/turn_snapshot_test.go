package chat_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/chat"
	ctxsnapshot "github.com/phlin/go-agent/internal/context"
	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/model"
	"github.com/phlin/go-agent/internal/qq"
	"github.com/phlin/go-agent/internal/replay"
)

type countingEmbedder struct{ calls atomic.Int32 }

func (e *countingEmbedder) Embed(context.Context, string) ([]float32, error) {
	e.calls.Add(1)
	return []float32{1, 0, 0}, nil
}
func (e *countingEmbedder) EmbeddingVersion() string { return "v1" }

func imagePayload(id, group, user int, text, url string) []byte {
	message := []map[string]any{
		{"type": "text", "data": map[string]any{"text": text}},
		{"type": "at", "data": map[string]any{"qq": "99"}},
		{"type": "image", "data": map[string]any{"url": url}},
	}
	b, _ := json.Marshal(map[string]any{"post_type": "message", "message_type": "group", "self_id": 99, "group_id": group, "user_id": user, "message_id": id, "message": message})
	return b
}

func replyPayload(id, group, user int, replyTo, text string) []byte {
	message := []map[string]any{
		{"type": "reply", "data": map[string]any{"id": replyTo}},
		{"type": "text", "data": map[string]any{"text": text}},
		{"type": "at", "data": map[string]any{"qq": "99"}},
	}
	b, _ := json.Marshal(map[string]any{"post_type": "message", "message_type": "group", "self_id": 99, "group_id": group, "user_id": user, "message_id": id, "message": message})
	return b
}

func setupWith(t *testing.T, generator model.Generator, store chat.Store) *chat.Runtime {
	t.Helper()
	r := chat.New(context.Background(), &chat.Runtime{Store: store, Model: generator, Sender: &replay.Sender{}, SelfID: 99, Persona: chat.Persona{Name: "芙芙"},
		Config: chat.Config{Merge: 10 * time.Millisecond, MaxWait: 200 * time.Millisecond, PassiveInterval: time.Millisecond, ActiveWindow: time.Second, Timeout: time.Second, SpontaneousInterval: time.Hour, MaxPerMinute: 20, History: 80, ContextRunes: 20000}})
	t.Cleanup(r.Close)
	return r
}

func TestTurnUsesOneQueryEmbeddingAcrossMemoryAndModel(t *testing.T) {
	store := replay.NewStore()
	embedder := &countingEmbedder{}
	r := setupWith(t, generator{func(context.Context, any) (chat.Reply, error) {
		return chat.Reply{Action: "silent"}, nil
	}}, store)
	r.Embedding = embedder
	r.Memory = &memory.Service{Store: store, Embedding: embedder, TopK: 3, Budgets: memory.RecallBudgets{Preference: 6, Member: 8, Episode: 6}}
	a, err := r.Process(context.Background(), payload(1, 1, 10, "最近怎么样", true))
	if err != nil || a.State != "silent" {
		t.Fatal(a, err)
	}
	if calls := embedder.calls.Load(); calls != 1 {
		t.Fatalf("query embedding calls = %d, want 1", calls)
	}
}

func TestBudgetIsCheckedBeforeEmbeddingAndModel(t *testing.T) {
	var generated atomic.Int32
	store := replay.NewStore()
	embedder := &countingEmbedder{}
	r := setupWith(t, generator{func(context.Context, any) (chat.Reply, error) {
		generated.Add(1)
		return chat.Reply{Action: "text", Text: "在"}, nil
	}}, store)
	r.Embedding = embedder
	r.Config.MaxPerMinute = 1
	if a, err := r.Process(context.Background(), payload(1, 1, 10, "第一句", true)); err != nil || a.State != "confirmed" {
		t.Fatal(a, err)
	}
	a, err := r.Process(context.Background(), payload(2, 1, 10, "第二句", true))
	if err != nil || a.State != "limited" {
		t.Fatal("budget was not enforced before model work", a, err)
	}
	if generated.Load() != 1 || embedder.calls.Load() != 1 {
		t.Fatalf("limited turn consumed capacity: model=%d embedding=%d", generated.Load(), embedder.calls.Load())
	}
	if a.Retrieval.PolicyReason != "limited" {
		t.Fatalf("policy reason = %q", a.Retrieval.PolicyReason)
	}
}

func TestImagesAreCachedPerURL(t *testing.T) {
	var fetches atomic.Int32
	r := setupWith(t, generator{func(context.Context, any) (chat.Reply, error) {
		return chat.Reply{Action: "silent"}, nil
	}}, replay.NewStore())
	r.Config.Cache.Image = time.Minute
	r.ImageFetcher = func(context.Context, string) (string, error) {
		fetches.Add(1)
		return "data:image/png;base64,AAAA", nil
	}
	for i := 1; i <= 2; i++ {
		a, err := r.Process(context.Background(), imagePayload(i, 1, 10, fmt.Sprint("看图", i), "https://media.example/one.png"))
		if err != nil || a.State != "silent" {
			t.Fatal(a, err)
		}
	}
	if fetches.Load() != 1 {
		t.Fatalf("image downloads = %d, want 1", fetches.Load())
	}
}

func TestImageFailureDegradesWithoutFailingTheTurn(t *testing.T) {
	r := setupWith(t, generator{func(context.Context, any) (chat.Reply, error) {
		return chat.Reply{Action: "silent"}, nil
	}}, replay.NewStore())
	r.ImageFetcher = func(context.Context, string) (string, error) {
		return "", fmt.Errorf("media unavailable")
	}
	a, err := r.Process(context.Background(), imagePayload(1, 1, 10, "看图", "https://media.example/broken.png"))
	if err != nil || a.State != "silent" {
		t.Fatal(a, err)
	}
	found := false
	for _, reason := range a.Retrieval.Degraded {
		found = found || reason == "images"
	}
	if !found {
		t.Fatalf("image failure was not recorded: %+v", a.Retrieval)
	}
}

type recordingBuilder struct {
	calls     atomic.Int32
	admission ctxsnapshot.AdmissionSnapshot
}

func (b *recordingBuilder) Build(_ context.Context, admission ctxsnapshot.AdmissionSnapshot) (ctxsnapshot.ContextSnapshot, error) {
	b.calls.Add(1)
	b.admission = admission
	return ctxsnapshot.ContextSnapshot{
		GroupID:        admission.GroupID,
		Version:        admission.ActorVersion,
		History:        admission.History,
		Batch:          admission.Batch,
		Conversation:   admission.Conversation,
		Query:          admission.Query,
		Notes:          []memory.Note{{ID: "snapshot-only", Text: "来自自定义构建器"}},
		StickerAssets:  nil,
		Images:         nil,
		QueryEmbedding: []float32{1},
	}, nil
}

func TestRuntimeUsesInjectedContextBuilder(t *testing.T) {
	var notes atomic.Bool
	builder := &recordingBuilder{}
	r := setupWith(t, generator{func(_ context.Context, input any) (chat.Reply, error) {
		values := input.(map[string]any)
		notes.Store(len(values["notes"].([]memory.Note)) == 1)
		return chat.Reply{Action: "silent"}, nil
	}}, replay.NewStore())
	r.ContextBuilder = builder
	if _, err := r.Process(context.Background(), payload(1, 1, 10, "你好", true)); err != nil {
		t.Fatal(err)
	}
	if builder.calls.Load() != 1 || !notes.Load() {
		t.Fatalf("custom builder was not used: calls=%d notes=%v", builder.calls.Load(), notes.Load())
	}
	if builder.admission.Query == "" || len(builder.admission.Batch) != 1 {
		t.Fatalf("admission snapshot incomplete: %+v", builder.admission)
	}
}

func TestTopicIDsSurviveRestartThroughReplyChain(t *testing.T) {
	store := replay.NewStore()
	topics := map[string]string{}
	capture := func(input any) string {
		batch := input.(map[string]any)["batch"].([]qq.Event)
		conversation := input.(map[string]any)["conversation_context"].(chat.ConversationContext)
		for _, event := range conversation.Batch {
			if event.EventID == batch[0].ID && len(event.TopicIDs) > 0 {
				topics[batch[0].Text] = event.TopicIDs[0]
				return event.TopicIDs[0]
			}
		}
		return ""
	}
	first := setupWith(t, generator{func(_ context.Context, input any) (chat.Reply, error) {
		capture(input)
		batch := input.(map[string]any)["batch"].([]qq.Event)
		return chat.Reply{Action: "text", Text: "好", Plan: &chat.ReplyPlan{TargetEventID: batch[0].ID, ResponseMode: "short"}}, nil
	}}, store)
	if a, err := first.Process(context.Background(), payload(1, 1, 10, "周末去爬山吗", true)); err != nil || a.State != "confirmed" {
		t.Fatal(a, err)
	}
	if a, err := first.Process(context.Background(), payload(2, 1, 11, "今晚吃火锅", true)); err != nil || a.State != "confirmed" {
		t.Fatal(a, err)
	}
	first.Close()
	if topics["周末去爬山吗"] == "" || topics["今晚吃火锅"] == "" || topics["周末去爬山吗"] == topics["今晚吃火锅"] {
		t.Fatalf("fixture topics = %+v", topics)
	}

	var restored string
	second := setupWith(t, generator{func(_ context.Context, input any) (chat.Reply, error) {
		restored = capture(input)
		return chat.Reply{Action: "silent"}, nil
	}}, store)
	if a, err := second.Process(context.Background(), replyPayload(3, 1, 12, "2", "火锅还是烧烤")); err != nil || a.State != "silent" {
		t.Fatal(a, err)
	}
	if restored != topics["今晚吃火锅"] {
		t.Fatalf("topic after restart = %q, want %q", restored, topics["今晚吃火锅"])
	}

	var fresh string
	third := setupWith(t, generator{func(_ context.Context, input any) (chat.Reply, error) {
		fresh = capture(input)
		return chat.Reply{Action: "silent"}, nil
	}}, store)
	if a, err := third.Process(context.Background(), payload(4, 1, 13, "最近怎么样", true)); err != nil || a.State != "silent" {
		t.Fatal(a, err)
	}
	if fresh == "" || fresh == topics["周末去爬山吗"] || fresh == topics["今晚吃火锅"] {
		t.Fatalf("new topic reused a restored id: %q", fresh)
	}
}
