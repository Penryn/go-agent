package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/chat"
	"github.com/phlin/go-agent/internal/meme"
	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/model"
	"github.com/phlin/go-agent/internal/qq"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type classifier struct{ calls int }

func (c *classifier) Generate(context.Context, string, any, []string) (model.Result, error) {
	c.calls++
	return model.Result{Content: `{"collect":true,"description":"无语","tags":["无语","沉默"]}`}, nil
}
func TestAutomaticCollectionDeduplicatesAcrossGroupsAndRespectsBlock(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(imageBytes.Bytes())), Header: http.Header{}}, nil
	})}
	classifier := &classifier{}
	service := meme.Service{Store: s, Model: classifier, HTTP: httpClient, Config: meme.Config{AutoCollect: true, Path: t.TempDir(), MaxItems: 10, MaxBytes: 2 << 20, MaxImageBytes: 1 << 20, TopK: 3, Cooldown: time.Minute}}
	stage := func(id string, group int64) {
		t.Helper()
		if err := service.Observe(ctx, qq.Event{ID: id, GroupID: group, Images: []qq.Attachment{{ID: id + "-image", URL: "https://media.example/" + id, Sticker: true}}}); err != nil {
			t.Fatal(err)
		}
		if err := service.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	stage("from-A", 1)
	stage("from-B", 2)
	out, err := service.Candidates(ctx, 999, "无语")
	if err != nil || len(out) != 1 || classifier.calls != 1 {
		t.Fatal("automatic global collection failed", out, classifier.calls, err)
	}
	if err := s.DisableSticker(ctx, out[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(out[0].Path); err != nil {
		t.Fatal(err)
	}
	stage("from-C", 3)
	if _, err := os.Stat(out[0].Path); !os.IsNotExist(err) {
		t.Fatal("blocked image file was automatically recreated", err)
	}
	out, err = service.Candidates(ctx, 2, "无语")
	if err != nil || len(out) != 0 {
		t.Fatal("blocked content was collected again", out, err)
	}
}

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	required := dsn != ""
	if dsn == "" {
		dsn = "postgres://qqbot:qqbotpass@127.0.0.1:5432/postgres?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", dsn)
	if err == nil {
		err = admin.PingContext(ctx)
	}
	if err != nil {
		if admin != nil {
			admin.Close()
		}
		if required {
			t.Fatal(err)
		}
		t.Skipf("PostgreSQL unavailable: %v", err)
	}
	name := fmt.Sprintf("groupfriend_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + name
	store, err := Open(context.Background(), parsed.String(), 3)
	if err != nil {
		admin.ExecContext(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		_, err := admin.ExecContext(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close()
		if err != nil {
			t.Error(err)
		}
	})
	return store
}
func event(t *testing.T, s *Store, id string, group, user int64, text string) qq.Event {
	t.Helper()
	e := qq.Event{ID: id, MessageID: id, GroupID: group, UserID: user, Text: text, At: time.Now()}
	inserted, err := s.Ingest(context.Background(), &e)
	if err != nil || !inserted {
		t.Fatalf("ingest: %v %v", inserted, err)
	}
	return e
}
func TestMessageIdentityAndRecall(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	e := event(t, s, "a", 1, 10, "哈哈")
	again := e
	if ok, err := s.Ingest(ctx, &again); err != nil || ok {
		t.Fatalf("duplicate accepted: %v %v", ok, err)
	}
	event(t, s, "b", 1, 11, "哈哈")
	event(t, s, "c", 2, 11, "other group")
	recall := qq.Event{ID: "recall-a", MessageID: "a", GroupID: 1, UserID: 10, Recalled: true, At: time.Now()}
	if _, err := s.Ingest(ctx, &recall); err != nil {
		t.Fatal(err)
	}
	all, err := s.Recent(ctx, 1, 20)
	if err != nil || len(all) != 3 || !all[0].Recalled || all[1].Text != "哈哈" {
		t.Fatalf("wrong history: %+v %v", all, err)
	}
}
func TestMemoryEvidenceVersionsAndIndexInvalidation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	svc := memory.Service{Store: s, TopK: 4}
	e := event(t, s, "e1", 1, 10, "记住我喜欢星际探索")
	ch := memory.Change{Subject: 10, Kind: "preference", Key: "interest", Text: "喜欢星际探索", Evidence: []memory.Evidence{{EventID: e.ID, Quote: "喜欢星际探索"}}}
	if err := svc.Apply(ctx, 1, []qq.Event{e}, []memory.Change{ch}, nil); err != nil {
		t.Fatal(err)
	}
	notes, _ := s.Notes(ctx, 1, 10)
	if len(notes) != 1 {
		t.Fatal(notes)
	}
	n := notes[0]
	if err := s.IndexNote(ctx, n, []float32{1, 0, 0}, "v1"); err != nil {
		t.Fatal(err)
	}
	matched, err := s.VectorNotes(ctx, 1, []float32{1, 0, 0}, "v1", 5)
	if err != nil || len(matched) != 1 {
		t.Fatalf("vector recall: %v %v", matched, err)
	}
	foreign, err := s.VectorNotes(ctx, 2, []float32{1, 0, 0}, "v1", 5)
	if err != nil || len(foreign) != 0 {
		t.Fatal("cross-group memory leak", err)
	}
	bad := ch
	bad.Subject = 99
	if err := svc.Apply(ctx, 1, []qq.Event{e}, []memory.Change{bad}, nil); err == nil {
		t.Fatal("member authority was not enforced")
	}
	forged := ch
	forged.Evidence = []memory.Evidence{{EventID: e.ID, Quote: "不存在的原话"}}
	if err := svc.Apply(ctx, 1, []qq.Event{e}, []memory.Change{forged}, nil); err == nil {
		t.Fatal("forged evidence accepted")
	}
	forget := event(t, s, "e2", 1, 10, "忘掉这个兴趣")
	ch.ID = n.ID
	ch.Version = n.Version
	ch.Forget = true
	ch.Text = ""
	ch.Evidence = []memory.Evidence{{EventID: forget.ID, Quote: forget.Text}}
	if err := svc.Apply(ctx, 1, []qq.Event{forget}, []memory.Change{ch}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.IndexNote(ctx, n, []float32{1, 0, 0}, "v1"); err != nil {
		t.Fatal(err)
	}
	matched, err = s.VectorNotes(ctx, 1, []float32{1, 0, 0}, "v1", 5)
	if err != nil || len(matched) != 0 {
		t.Fatal("old vector revived forgotten memory", err)
	}
	recreated := memory.Change{Subject: 10, Kind: "member", Key: "new_alias_for_old_fact", Text: "喜欢星际探索", Evidence: []memory.Evidence{{EventID: e.ID, Quote: "喜欢星际探索"}}}
	p := memory.Progress{GroupID: 1, After: 0, Until: forget.Seq}
	if err := svc.Apply(ctx, 1, []qq.Event{e}, []memory.Change{recreated}, &p); err == nil {
		t.Fatal("old evidence recreated a forgotten fact")
	}
}
func TestLearningCursorCommitsWithNotes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	e := event(t, s, "e", 3, 10, "我喜欢音乐")
	events, p, err := s.LearningBatch(ctx, 40)
	if err != nil || len(events) != 1 {
		t.Fatal(events, p, err)
	}
	svc := memory.Service{Store: s}
	ch := memory.Change{Kind: "member", Subject: 10, Key: "interest", Text: "喜欢音乐", Evidence: []memory.Evidence{{EventID: e.ID, Quote: "喜欢音乐"}}}
	wrong := p
	wrong.After = 99
	if err := svc.Apply(ctx, 3, events, []memory.Change{ch}, &wrong); err == nil {
		t.Fatal("stale cursor accepted")
	}
	notes, _ := s.Notes(ctx, 3, 10)
	if len(notes) != 0 {
		t.Fatal("partial note write was committed")
	}
	if err := svc.Apply(ctx, 3, events, []memory.Change{ch}, &p); err != nil {
		t.Fatal(err)
	}
	events, _, err = s.LearningBatch(ctx, 40)
	if err != nil || len(events) != 0 {
		t.Fatal("cursor not advanced", events, err)
	}
}
func TestGlobalStickersAndPerGroupCooldown(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := meme.Asset{ID: "a", SourceID: "e1", SourceGroup: 1, URL: "https://example.com/a", State: "pending", LastSeen: time.Now()}
	if err := s.StageSticker(ctx, a); err != nil {
		t.Fatal(err)
	}
	pending, _ := s.PendingStickers(ctx, 10)
	a = pending[0]
	a.Hash = "same-content"
	a.Path = "/tmp/test-image"
	a.MIME = "image/png"
	a.Size = 10
	var err error
	a, err = s.CacheSticker(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	a.State = "ready"
	a.Description = "无语"
	a.Tags = []string{"无语"}
	if err := s.DescribeSticker(ctx, a); err != nil {
		t.Fatal(err)
	}
	b := meme.Asset{ID: "b", SourceID: "e2", SourceGroup: 2, URL: "https://example.com/b", LastSeen: time.Now()}
	if err := s.StageSticker(ctx, b); err != nil {
		t.Fatal(err)
	}
	pending, _ = s.PendingStickers(ctx, 10)
	b = pending[0]
	b.Hash = a.Hash
	b.Path = a.Path
	b.Size = 10
	merged, err := s.CacheSticker(ctx, b)
	if err != nil || merged.ID != a.ID {
		t.Fatal("global content dedup failed", merged, err)
	}
	for _, group := range []int64{1, 2, 999} {
		out, err := s.StickerCandidates(ctx, group, time.Minute, 20)
		if err != nil || len(out) != 1 {
			t.Fatal("global sticker not visible", group, out, err)
		}
	}
	data, _ := json.Marshal(merged)
	var view map[string]any
	_ = json.Unmarshal(data, &view)
	for _, key := range []string{"SourceGroup", "source_group", "SourceID", "source_id", "URL", "url"} {
		if _, ok := view[key]; ok {
			t.Fatal("internal provenance exposed to the model")
		}
	}
	attempt := chat.Attempt{ID: "send", GroupID: 1, InputSeq: 1, State: "planning", At: time.Now(), Reply: chat.Reply{Action: "sticker", StickerID: a.ID}}
	if ok, err := s.BeginAttempt(ctx, attempt); err != nil || !ok {
		t.Fatal(err)
	}
	attempt.State = "sending"
	if err := s.UpdateAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	attempt.State = "confirmed"
	if err := s.FinishAttempt(ctx, attempt, qq.Event{ID: "out", GroupID: 1, MessageID: "100", Outbound: true, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	same, _ := s.StickerCandidates(ctx, 1, time.Minute, 20)
	other, _ := s.StickerCandidates(ctx, 2, time.Minute, 20)
	if len(same) != 0 || len(other) != 1 {
		t.Fatal("cooldown must be per group", same, other)
	}
	if err := s.DisableSticker(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	out, _ := s.StickerCandidates(ctx, 2, time.Minute, 20)
	if len(out) != 0 {
		t.Fatal("disabled sticker remains available")
	}
}
func TestSenderOwnershipAndCrashRecovery(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := chat.Attempt{ID: "unknown", GroupID: 1, State: "planning", At: time.Now()}
	_, _ = s.BeginAttempt(ctx, a)
	a.State = "sending"
	_ = s.UpdateAttempt(ctx, a)
	if err := s.AcquireSender(ctx); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := s.DB.QueryRowContext(ctx, `SELECT state FROM groupfriend.reply_attempts WHERE id='unknown'`).Scan(&state); err != nil || state != "unknown" {
		t.Fatal(state, err)
	}
	other := &Store{DB: s.DB}
	if err := other.AcquireSender(ctx); err == nil {
		t.Fatal("second sending owner admitted")
	}
	if ok, err := s.BeginAttempt(ctx, a); err != nil || ok {
		t.Fatal("duplicate send attempt admitted", err)
	}
}
