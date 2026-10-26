package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/chat"
	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/model"
	"github.com/phlin/go-agent/internal/qq"
	"github.com/phlin/go-agent/internal/replay"
)

type generator struct {
	fn func(context.Context, any) (chat.Reply, error)
}

func (g generator) Generate(ctx context.Context, _ string, input any, _ []string) (model.Result, error) {
	r, err := g.fn(ctx, input)
	b, _ := json.Marshal(r)
	return model.Result{Content: string(b)}, err
}
func payload(id, group, user int, text string, directed bool) []byte {
	message := []map[string]any{{"type": "text", "data": map[string]any{"text": text}}}
	if directed {
		message = append(message, map[string]any{"type": "at", "data": map[string]any{"qq": "99"}})
	}
	b, _ := json.Marshal(map[string]any{"post_type": "message", "message_type": "group", "self_id": 99, "group_id": group, "user_id": user, "message_id": id, "message": message})
	return b
}
func setup(t *testing.T, generator model.Generator) (*chat.Runtime, *replay.Store, *replay.Sender) {
	t.Helper()
	store := replay.NewStore()
	sender := &replay.Sender{}
	r := chat.New(context.Background(), &chat.Runtime{Store: store, Model: generator, Sender: sender, SelfID: 99, Persona: chat.Persona{Name: "芙芙"}, // A burst must merge into one batch; the window stays wide enough to hold
		// several sequential submits even when the suite runs under parallel load.
		Config: chat.Config{Merge: 50 * time.Millisecond, MaxWait: 200 * time.Millisecond, PassiveInterval: time.Millisecond, ActiveWindow: time.Second, Timeout: time.Second, SpontaneousInterval: time.Hour, MaxPerMinute: 20, History: 80, ContextRunes: 20000}})
	t.Cleanup(r.Close)
	return r, store, sender
}
func wait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}
func TestBurstKeepsAuthorsAndWholeMeaning(t *testing.T) {
	seen := make(chan []qq.Event, 1)
	r, _, sender := setup(t, generator{func(ctx context.Context, input any) (chat.Reply, error) {
		batch := input.(map[string]any)["batch"].([]qq.Event)
		seen <- batch
		return chat.Reply{Action: "text", Text: "先别动，删的是哪个库", Reason: "direct"}, nil
	}})
	for i, text := range []string{"我刚才", "把数据库", "删了"} {
		if err := r.Submit(context.Background(), payload(i+1, 1, 10, text, true)); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case batch := <-seen:
		if len(batch) != 3 || batch[0].Text != "我刚才" || batch[2].Text != "删了" || batch[0].UserID != 10 {
			t.Fatal(batch)
		}
	case <-time.After(time.Second):
		t.Fatal("not called")
	}
	r.Close()
	if len(sender.Messages) > 1 {
		t.Fatal("multiple replies to one burst")
	}
}
func TestOrdinaryNewMessageDoesNotCancelGeneration(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	var cancelled atomic.Bool
	r, store, _ := setup(t, generator{func(ctx context.Context, input any) (chat.Reply, error) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-ctx.Done():
				cancelled.Store(true)
				return chat.Reply{}, ctx.Err()
			case <-release:
			}
			return chat.Reply{Action: "text", Text: "原问题的回答"}, nil
		}
		return chat.Reply{Action: "silent"}, nil
	}})
	done := make(chan chat.Attempt, 1)
	go func() { a, _ := r.Process(context.Background(), payload(1, 1, 10, "问你个问题", true)); done <- a }()
	wait(t, started)
	if err := r.Submit(context.Background(), payload(2, 1, 11, "其他群友在聊天", false)); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case a := <-done:
		if a.State != "confirmed" || cancelled.Load() {
			t.Fatal(a, cancelled.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("not finished")
	}
	events, _ := store.Recent(context.Background(), 1, 100)
	out := 0
	for _, e := range events {
		if e.Outbound {
			out++
		}
	}
	if out != 1 {
		t.Fatal("confirmed output missing", events)
	}
}
func TestNewDirectMessageInvalidatesOldDraft(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	r, _, sender := setup(t, generator{func(ctx context.Context, input any) (chat.Reply, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return chat.Reply{Action: "text", Text: "过时草稿"}, nil
		}
		return chat.Reply{Action: "text", Text: "更新后的回答"}, nil
	}})
	first := make(chan chat.Attempt, 1)
	go func() {
		a, _ := r.Process(context.Background(), payload(1, 1, 10, "第一个问题", true))
		first <- a
	}()
	wait(t, started)
	second := make(chan chat.Attempt, 1)
	go func() { a, _ := r.Process(context.Background(), payload(2, 1, 10, "问题改了", true)); second <- a }()
	// Wait for durable admission instead of relying on a scheduling sleep.
	deadline := time.Now().Add(time.Second)
	for {
		events, _ := r.Store.Recent(context.Background(), 1, 20)
		if len(events) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second event not admitted")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(2 * time.Millisecond)
	close(release)
	if a := <-first; a.State != "stale" {
		t.Fatal(a)
	}
	if a := <-second; a.State != "confirmed" {
		t.Fatal(a)
	}
	r.Close()
	if len(sender.Messages) != 1 || sender.Messages[0] != "更新后的回答" {
		t.Fatal(sender.Messages)
	}
}
func TestFollowupBypassesSpontaneousInterval(t *testing.T) {
	r, _, _ := setup(t, generator{func(context.Context, any) (chat.Reply, error) {
		return chat.Reply{Action: "text", Text: "接着说"}, nil
	}})
	for i := 1; i <= 2; i++ {
		a, err := r.Process(context.Background(), payload(i, 1, 10, fmt.Sprint("继续", i), i == 1))
		if err != nil || a.State != "confirmed" {
			t.Fatal(a, err)
		}
	}
	a, err := r.Process(context.Background(), payload(3, 1, 11, "另一位群友的闲聊", false))
	if err != nil || a.State != "limited" {
		t.Fatal("unrelated speaker bypassed interval", a, err)
	}
}
func TestSendTimeoutDoesNotCreateHistoryOrRetry(t *testing.T) {
	r, store, sender := setup(t, generator{func(context.Context, any) (chat.Reply, error) { return chat.Reply{Action: "text", Text: "草稿"}, nil }})
	sender.Error = &qq.SendError{Err: context.DeadlineExceeded, Definite: false}
	a, err := r.Process(context.Background(), payload(1, 1, 10, "测试", true))
	if err != nil || a.State != "unknown" {
		t.Fatal(a, err)
	}
	a, err = r.Process(context.Background(), payload(1, 1, 10, "测试", true))
	if err != nil || a.State != "duplicate" {
		t.Fatal(a, err)
	}
	events, _ := store.Recent(context.Background(), 1, 50)
	if len(events) != 1 || events[0].Outbound {
		t.Fatal("phantom history", events)
	}
}
func TestCloseCancelsAndWaitsForInflightModel(t *testing.T) {
	started := make(chan struct{})
	r, _, _ := setup(t, generator{func(ctx context.Context, _ any) (chat.Reply, error) {
		close(started)
		<-ctx.Done()
		return chat.Reply{}, ctx.Err()
	}})
	if err := r.Submit(context.Background(), payload(1, 1, 10, "hello", true)); err != nil {
		t.Fatal(err)
	}
	wait(t, started)
	done := make(chan struct{})
	go func() { r.Close(); close(done) }()
	wait(t, done)
	if err := r.Submit(context.Background(), payload(2, 1, 10, "after close", true)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestDifferentGroupsGenerateConcurrently(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(2)
	release := make(chan struct{})
	started := make(chan struct{})
	r, _, _ := setup(t, generator{func(ctx context.Context, input any) (chat.Reply, error) {
		wg.Done()
		<-release
		return chat.Reply{Action: "silent"}, nil
	}})
	go func() { wg.Wait(); close(started) }()
	for _, group := range []int{1, 2} {
		if err := r.Submit(context.Background(), payload(group, group, 10, "hello", true)); err != nil {
			t.Fatal(err)
		}
	}
	wait(t, started)
	close(release)
}
func TestExplicitMemoryRequiresOwnEvidence(t *testing.T) {
	r, store, _ := setup(t, generator{func(ctx context.Context, input any) (chat.Reply, error) {
		e := input.(map[string]any)["batch"].([]qq.Event)[0]
		return chat.Reply{Action: "text", Text: "记住了", Memory: []memory.Change{{Kind: "preference", Subject: 999, Key: "nickname", Text: "某人", Evidence: []memory.Evidence{{EventID: e.ID, Quote: e.Text}}}}}, nil
	}})
	r.Memory = &memory.Service{Store: store, TopK: 3}
	a, err := r.Process(context.Background(), payload(1, 1, 10, "以后叫我小明", true))
	if err != nil || a.State != "failed" || !strings.Contains(a.Error, "memory update failed") {
		t.Fatal(a, err)
	}
	events, _ := store.Recent(context.Background(), 1, 20)
	if len(events) != 1 {
		t.Fatal("false success confirmation was sent")
	}
}
