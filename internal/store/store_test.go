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
	"github.com/phlin/go-agent/internal/conversation"
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

func TestTopicIDsReturnsPersistedActionableAssignments(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	e := event(t, s, "topic-event", 1, 10, "周末去爬山吗")
	err := s.SaveTopics(ctx, 1, nil, []conversation.TopicEventView{{
		TopicID:   "topic:1",
		EventID:   e.ID,
		MessageID: e.MessageID,
		Seq:       e.Seq,
		TopicIDs:  []string{"topic:1", "topic:2"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	topics, err := s.TopicIDs(ctx, 1, []string{e.ID, "unknown"})
	if err != nil || len(topics[e.ID]) != 2 || topics[e.ID][0] != "topic:1" || topics[e.ID][1] != "topic:2" {
		t.Fatalf("topic assignments = %+v err=%v", topics, err)
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
	notes, _ := s.ActiveNotes(ctx, 1, 10)
	if len(notes) != 1 {
		t.Fatal(notes)
	}
	n := notes[0]
	if err := s.IndexNote(ctx, n, []float32{1, 0, 0}, "v1"); err != nil {
		t.Fatal(err)
	}
	matched, err := s.VectorNotes(ctx, 1, []float32{1, 0, 0}, "v1", 5, 0.6)
	if err != nil || len(matched) != 1 {
		t.Fatalf("vector recall: %v %v", matched, err)
	}
	foreign, err := s.VectorNotes(ctx, 2, []float32{1, 0, 0}, "v1", 5, 0.6)
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
	matched, err = s.VectorNotes(ctx, 1, []float32{1, 0, 0}, "v1", 5, 0.6)
	if err != nil || len(matched) != 0 {
		t.Fatal("old vector revived forgotten memory", err)
	}
	recreated := memory.Change{Subject: 10, Kind: "member", Key: "new_alias_for_old_fact", Text: "喜欢星际探索", Evidence: []memory.Evidence{{EventID: e.ID, Quote: "喜欢星际探索"}}}
	p := memory.Progress{GroupID: 1, After: 0, Until: forget.Seq}
	if err := svc.Apply(ctx, 1, []qq.Event{e}, []memory.Change{recreated}, &p); err == nil {
		t.Fatal("old evidence recreated a forgotten fact")
	}
	var blocked int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM groupfriend.memory_events WHERE kind='forgotten_blocked'`).Scan(&blocked); err != nil || blocked == 0 {
		t.Fatalf("blocked rebuild was not recorded: count=%d err=%v", blocked, err)
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
	notes, _ := s.ActiveNotes(ctx, 3, 10)
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

func TestEditNoteAdvancesMemoryRevision(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	e := event(t, s, "edit-e1", 1, 10, "记住我喜欢咖啡")
	svc := memory.Service{Store: s, TopK: 4}
	change := memory.Change{Subject: 10, Kind: "preference", Key: "drink", Text: "喜欢咖啡", Evidence: []memory.Evidence{{EventID: e.ID, Quote: "喜欢咖啡"}}}
	if err := svc.Apply(ctx, 1, []qq.Event{e}, []memory.Change{change}, nil); err != nil {
		t.Fatal(err)
	}
	before, err := s.MemoryRevision(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	notes, err := s.ActiveNotes(ctx, 1, 10)
	if err != nil || len(notes) != 1 {
		t.Fatalf("notes = %+v err=%v", notes, err)
	}
	if err := s.EditNote(ctx, 1, notes[0].ID, notes[0].Version, "现在喜欢茶", false); err != nil {
		t.Fatal(err)
	}
	after, err := s.MemoryRevision(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if after <= before {
		t.Fatalf("memory revision did not advance: before=%d after=%d", before, after)
	}
	edited, err := s.ActiveNotes(ctx, 1, 10)
	if err != nil || len(edited) != 1 || edited[0].Text != "现在喜欢茶" {
		t.Fatalf("edited note = %+v err=%v", edited, err)
	}
}
func TestMemoryReworkMigrationRunsOnce(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO groupfriend.notes(id,group_id,subject,kind,key,text,evidence,revision,source_seq,state,updated_at,confidence,use_count,usage_version) VALUES('legacy',1,10,'preference','nickname','称呼小林','[]',1,1,'active',NOW(),1,5,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM groupfriend.settings WHERE key='memory_rework_migration'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Initialize(ctx, 3); err != nil {
		t.Fatal(err)
	}
	notes, err := s.AllNotes(ctx, 1, 10)
	if err != nil || len(notes) != 1 || notes[0].Confidence != 0.7 || notes[0].UseCount != 0 || notes[0].UsageVersion != 0 {
		t.Fatalf("legacy row = %+v err=%v", notes, err)
	}
	// The marker makes the baseline one-time; later confidence=1 stays.
	if _, err := s.DB.ExecContext(ctx, `UPDATE groupfriend.notes SET confidence=1,use_count=3 WHERE id='legacy'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Initialize(ctx, 3); err != nil {
		t.Fatal(err)
	}
	notes, _ = s.AllNotes(ctx, 1, 10)
	if notes[0].Confidence != 1 || notes[0].UseCount != 3 {
		t.Fatalf("migration reran: %+v", notes[0])
	}
}

func TestConfirmedUsageLedgerIsIdempotent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	svc := memory.Service{Store: s, TopK: 4}
	create := func(group int64, eventID, text string) memory.Note {
		e := event(t, s, eventID, group, 10, "记住"+text)
		change := memory.Change{Subject: 10, Kind: "preference", Key: "interest", Text: text, Evidence: []memory.Evidence{{EventID: e.ID, Quote: text}}}
		if err := svc.Apply(ctx, group, []qq.Event{e}, []memory.Change{change}, nil); err != nil {
			t.Fatal(err)
		}
		notes, err := s.ActiveNotes(ctx, group, 10)
		if err != nil || len(notes) != 1 {
			t.Fatalf("notes = %+v err=%v", notes, err)
		}
		return notes[0]
	}
	note := create(1, "usage-a", "喜欢星际探索")
	foreign := create(2, "usage-b", "喜欢音乐")

	useCount := func(group int64, id string) memory.Note {
		notes, err := s.ActiveNotes(ctx, group, 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range notes {
			if n.ID == id {
				return n
			}
		}
		t.Fatalf("note %s missing", id)
		return memory.Note{}
	}
	ledgerRows := func(attempt string) int {
		var n int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM groupfriend.note_usage WHERE attempt_id=$1`, attempt).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	start := func(id string) chat.Attempt {
		a := chat.Attempt{ID: id, GroupID: 1, InputSeq: 1, State: "planning", At: time.Now()}
		if ok, err := s.BeginAttempt(ctx, a); err != nil || !ok {
			t.Fatal(ok, err)
		}
		a.State = "sending"
		if err := s.UpdateAttempt(ctx, a); err != nil {
			t.Fatal(err)
		}
		return a
	}

	first := start("usage-1")
	confirmed := qq.Event{ID: "out-usage-1", GroupID: 1, MessageID: "platform-1", Outbound: true, At: time.Now()}
	// Duplicate, cross-group and unknown note IDs are all filtered out.
	if err := s.FinishAttempt(ctx, first, confirmed, []string{note.ID, note.ID, foreign.ID, "missing"}); err != nil {
		t.Fatal(err)
	}
	if n := ledgerRows("usage-1"); n != 1 {
		t.Fatalf("ledger rows = %d, want 1", n)
	}
	if got := useCount(1, note.ID); got.UseCount != 1 || got.UsageVersion != 1 {
		t.Fatalf("projection = %+v", got)
	}
	if got := useCount(2, foreign.ID); got.UseCount != 0 {
		t.Fatalf("cross-group note was booked: %+v", got)
	}
	// A repeated finish cannot book again.
	first.State = "confirmed"
	if err := s.FinishAttempt(ctx, first, confirmed, []string{note.ID}); err == nil {
		t.Fatal("repeated finish was accepted")
	}
	if n := ledgerRows("usage-1"); n != 1 || useCount(1, note.ID).UseCount != 1 {
		t.Fatal("repeated finish changed the ledger")
	}
	// A second confirmed attempt books the same note once more.
	second := start("usage-2")
	if err := s.FinishAttempt(ctx, second, qq.Event{ID: "out-usage-2", GroupID: 1, MessageID: "platform-2", Outbound: true, At: time.Now()}, []string{note.ID}); err != nil {
		t.Fatal(err)
	}
	if n := ledgerRows("usage-2"); n != 1 || useCount(1, note.ID).UseCount != 2 {
		t.Fatal("second attempt did not book exactly once")
	}
	// A planning attempt cannot finish.
	planning := chat.Attempt{ID: "usage-planning", GroupID: 1, InputSeq: 2, State: "planning", At: time.Now()}
	if ok, err := s.BeginAttempt(ctx, planning); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := s.FinishAttempt(ctx, planning, qq.Event{ID: "out-planning", GroupID: 1, MessageID: "platform-3", Outbound: true, At: time.Now()}, []string{note.ID}); err == nil {
		t.Fatal("planning attempt finished")
	}
	if ledgerRows("usage-planning") != 0 {
		t.Fatal("planning attempt wrote usage")
	}
	// A failing outbound insert rolls back the confirmed state and the ledger.
	rollback := start("usage-rollback")
	if err := s.FinishAttempt(ctx, rollback, confirmed, []string{note.ID}); err == nil {
		t.Fatal("duplicate outbound event was accepted")
	}
	var state string
	if err := s.DB.QueryRowContext(ctx, `SELECT state FROM groupfriend.reply_attempts WHERE id='usage-rollback'`).Scan(&state); err != nil || state != "sending" {
		t.Fatalf("attempt state after rollback = %q err=%v", state, err)
	}
	if ledgerRows("usage-rollback") != 0 || useCount(1, note.ID).UseCount != 2 {
		t.Fatal("failed transaction left ledger or projection changes")
	}
	status, err := s.MemoryStatus(ctx)
	if err != nil || status.UsageProjectionDiff != 0 || len(status.Groups) == 0 || status.Groups[0].Lag <= 0 || status.Ledger.ConfirmedAttempts != 2 || status.Ledger.UsageRows != 2 {
		t.Fatalf("memory status = %+v err=%v", status, err)
	}
}

func TestIdlessCorrectionBindsCleanedLegacyIdentity(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	// The active identity index exists. The row keeps an ID derived from the
	// pre-normalization key, so an ID-less correction must bind it, not insert a
	// new normalized-ID row that collides with the index.
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO groupfriend.notes(id,group_id,subject,kind,key,text,evidence,revision,source_seq,state,updated_at,confidence) VALUES('legacy-nick',1,10,'preference','nickname','称呼小林','[]',1,1,'active',NOW(),0.7)`); err != nil {
		t.Fatal(err)
	}
	e := event(t, s, "p1-e1", 1, 10, "以后叫我阿林")
	svc := memory.Service{Store: s, TopK: 4}
	change := memory.Change{Subject: 10, Kind: "preference", Key: " NICKNAME ", Text: "称呼阿林", Evidence: []memory.Evidence{{EventID: e.ID, Quote: "以后叫我阿林"}}}
	if err := svc.Apply(ctx, 1, []qq.Event{e}, []memory.Change{change}, nil); err != nil {
		t.Fatal(err)
	}
	notes, err := s.ActiveNotes(ctx, 1, 10)
	if err != nil || len(notes) != 1 || notes[0].ID != "legacy-nick" || notes[0].Text != "称呼阿林" || notes[0].Version != 2 {
		t.Fatalf("bound correction = %+v err=%v", notes, err)
	}
}

func TestMemoryDuplicateCleanupIsDryRunFirst(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `DROP INDEX IF EXISTS groupfriend.friend_notes_active_identity`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	insert := func(id, key, state, text string, updated time.Time, evidence string) {
		t.Helper()
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO groupfriend.notes(id,group_id,subject,kind,key,topic_id,text,evidence,revision,source_seq,state,updated_at,confidence) VALUES($1,1,10,'preference',$2,'',$3,$4::jsonb,1,1,$5,$6,0.7)`, id, key, text, evidence, state, updated); err != nil {
			t.Fatal(err)
		}
	}
	insert("keep-old", "Nick", "active", "旧称呼", now.Add(-2*time.Hour), `[{"event_id":"e1","quote":"旧"}]`)
	insert("keep", " nick ", "active", "称呼阿林", now, `[{"event_id":"e2","quote":"阿林"},{"event_id":"e3","quote":"林"}]`)
	insert("stale", "NICK", "active", "称呼小林", now.Add(-time.Hour), `[]`)
	insert("forgot", "nick", "forgotten", "遗忘的称呼", now.Add(-time.Minute), `[{"event_id":"e4","quote":"忘"}]`)
	insert("other", "other", "active", "其他", now, `[]`)
	// Startup must not create the index while normalization is pending.
	if err := s.Initialize(ctx, 3); err != nil {
		t.Fatal(err)
	}
	// The reconciler can still find legacy rows through normalized identity.
	matched, err := s.NotesByIdentity(ctx, 1, []memory.Identity{{Subject: 10, Kind: "preference", Key: "nick"}})
	if err != nil || len(matched) != 4 {
		t.Fatalf("normalized identity lookup = %d err=%v", len(matched), err)
	}
	var exists bool
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE schemaname='groupfriend' AND indexname='friend_notes_active_identity')`).Scan(&exists); err != nil || exists {
		t.Fatalf("index created before cleanup: exists=%v err=%v", exists, err)
	}
	dry, err := s.MemoryDuplicates(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Applied || dry.IndexExists || len(dry.Normalized) != 3 || len(dry.Duplicates) != 1 {
		t.Fatalf("dry run = %+v", dry)
	}
	group := dry.Duplicates[0]
	if group.Keep.ID != "keep" || group.Keep.Evidence != 2 || group.Keep.Text != "称呼阿林" || len(group.Archive) != 2 {
		t.Fatalf("duplicate group = %+v", group)
	}
	var state, key string
	if err := s.DB.QueryRowContext(ctx, `SELECT state,key FROM groupfriend.notes WHERE id='keep-old'`).Scan(&state, &key); err != nil || state != "active" || key != "Nick" {
		t.Fatalf("dry run mutated data: state=%s key=%s err=%v", state, key, err)
	}
	report, err := s.MemoryDuplicates(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Applied || !report.IndexCreated || !report.IndexExists {
		t.Fatalf("apply report = %+v", report)
	}
	wantStates := map[string]string{"keep": "active", "keep-old": "archived", "stale": "archived", "forgot": "forgotten", "other": "active"}
	for id, want := range wantStates {
		if err := s.DB.QueryRowContext(ctx, `SELECT state FROM groupfriend.notes WHERE id=$1`, id).Scan(&state); err != nil || state != want {
			t.Fatalf("%s state=%s want=%s err=%v", id, state, want, err)
		}
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT key FROM groupfriend.notes WHERE id='keep'`).Scan(&key); err != nil || key != "nick" {
		t.Fatalf("normalized key=%s err=%v", key, err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO groupfriend.notes(id,group_id,subject,kind,key,topic_id,text,evidence,revision,source_seq,state,updated_at,confidence) VALUES('dup',1,10,'preference','nick','','重复','[]'::jsonb,1,1,'active',NOW(),0.7)`); err == nil {
		t.Fatal("active identity index did not reject a duplicate")
	}
}

func TestMemoryStatusAggregatesOperationalMetrics(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, run := range []memory.LearningRun{
		{GroupID: 1, UntilSeq: 5, StartedAt: time.Now(), DurationMS: 30, ExtractMS: 10, ReconcileMS: 20, ExtractTokens: 100, ReconcileTokens: 50, CandidateCount: 1, RejectedCount: 2, ChangeCount: 3, Status: "succeeded"},
		{GroupID: 1, UntilSeq: 6, StartedAt: time.Now(), DurationMS: 10, ExtractMS: 10, ExtractTokens: 20, Status: "failed", ErrorClass: "invalid_output"},
	} {
		if err := s.RecordLearningRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordMemoryEvent(ctx, 1, "forgotten_blocked"); err != nil {
		t.Fatal(err)
	}
	// This test needs duplicate active identities to exercise the metric.
	if _, err := s.DB.ExecContext(ctx, `DROP INDEX IF EXISTS groupfriend.friend_notes_active_identity`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"dup-a", "dup-b"} {
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO groupfriend.notes(id,group_id,subject,kind,key,text,evidence,revision,source_seq,state,updated_at,confidence) VALUES($1,1,10,'preference','nickname','称呼小林','[]',1,1,'active',NOW(),0.7)`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO groupfriend.reply_attempts(id,group_id,input_seq,state,at,body) VALUES('status-recall',1,1,'silent',NOW(),'{"retrieval":{"recall_attempted":true,"recall_hits":1,"recall_cache_hit":true,"degraded":["semantic"],"index_load_ms":250}}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO groupfriend.reply_attempts(id,group_id,input_seq,state,at,body) VALUES('status-fail',1,2,'unknown',NOW(),'{"error":"delivery confirmed but archive failed"}')`); err != nil {
		t.Fatal(err)
	}
	status, err := s.MemoryStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Learning.Runs != 2 || status.Learning.Succeeded != 1 || status.Learning.Failed != 1 || status.Learning.AvgDurationMS != 20 || status.Learning.AvgExtractMS != 10 || status.Learning.AvgReconcileMS != 10 || status.Learning.ExtractTokens != 120 || status.Learning.ReconcileTokens != 50 || status.Learning.RejectedCount != 2 || status.Learning.ChangeCount != 3 {
		t.Fatalf("learning stats = %+v", status.Learning)
	}
	if status.Recall.Attempted != 1 || status.Recall.Hits != 1 || status.Recall.Empty != 0 || status.Recall.CacheHits != 1 || status.Recall.CacheMisses != 0 || status.Recall.SemanticDegraded != 1 || status.Recall.HitRate != 1 || status.Recall.EmptyRate != 0 || status.Recall.CacheHitRate != 1 || status.Recall.MaxIndexLoadMS != 250 {
		t.Fatalf("recall stats = %+v", status.Recall)
	}
	if status.Ledger.WriteFailures != 1 || status.Ledger.WriteRetries != 0 {
		t.Fatalf("ledger stats = %+v", status.Ledger)
	}
	if status.Notes.DuplicateIdentities != 1 || status.Notes.TotalIdentities != 1 || status.Notes.DuplicateRate != 1 || status.Notes.ForgottenRebuilds != 1 || status.Notes.ForgottenRebuildRate != 0.25 {
		t.Fatalf("note stats = %+v", status.Notes)
	}
	if status.Notes.ActiveNotes != 2 || status.Notes.TotalNotes != 2 || status.Notes.ActiveRatio != 1 || status.Notes.ActiveIdentityIndex {
		t.Fatalf("note counts = %+v", status.Notes)
	}
	if status.PendingEmbeddings != 2 || status.UsageProjectionDiff != 0 {
		t.Fatalf("projection stats = %+v", status)
	}
}

func TestStaleNoteRevisionCannotOverwrite(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	svc := memory.Service{Store: s, TopK: 4}
	first := event(t, s, "rev-e1", 1, 10, "记住我喜欢星际探索")
	change := memory.Change{Subject: 10, Kind: "preference", Key: "interest", Text: "喜欢星际探索", Evidence: []memory.Evidence{{EventID: first.ID, Quote: "喜欢星际探索"}}}
	if err := svc.Apply(ctx, 1, []qq.Event{first}, []memory.Change{change}, nil); err != nil {
		t.Fatal(err)
	}
	notes, _ := s.ActiveNotes(ctx, 1, 10)
	stale := notes[0]
	second := event(t, s, "rev-e2", 1, 10, "记住我现在喜欢深海探索")
	change.ID, change.Version = stale.ID, stale.Version
	change.Text = "喜欢深海探索"
	change.Evidence = []memory.Evidence{{EventID: second.ID, Quote: "喜欢深海探索"}}
	if err := svc.Apply(ctx, 1, []qq.Event{second}, []memory.Change{change}, nil); err != nil {
		t.Fatal(err)
	}
	stale.Text = "过时覆盖"
	if err := s.SaveNotes(ctx, 1, []memory.Note{stale}, nil); err == nil {
		t.Fatal("stale revision overwrote the newer note")
	}
	notes, _ = s.ActiveNotes(ctx, 1, 10)
	if notes[0].Text != "喜欢深海探索" || notes[0].Version != stale.Version+1 {
		t.Fatalf("note = %+v", notes[0])
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
	if err := s.FinishAttempt(ctx, attempt, qq.Event{ID: "out", GroupID: 1, MessageID: "100", Outbound: true, At: time.Now()}, nil); err != nil {
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

func TestTopicEventsAndSummaryCursor(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	e := event(t, s, "topic-event", 1, 10, "周末去爬山")
	views := []conversation.TopicView{{ID: "topic:1:1", Participants: []int64{10}, FirstSeq: e.Seq, LastSeq: e.Seq, Revision: 2, LastActiveAt: time.Now(), Status: "active"}}
	events := []conversation.TopicEventView{{TopicID: "topic:1:1", EventID: e.ID, MessageID: e.MessageID, Seq: e.Seq, AssignedAt: time.Now(), TopicIDs: []string{"topic:1:1"}}}
	if err := s.SaveTopics(ctx, 1, views, events); err != nil {
		t.Fatal(err)
	}
	loadedViews, err := s.LoadTopics(ctx, 1)
	if err != nil || len(loadedViews) != 1 || loadedViews[0].ID != "topic:1:1" || loadedViews[0].Revision != 2 {
		t.Fatalf("restored topics = %+v err=%v", loadedViews, err)
	}
	loadedEvents, err := s.LoadTopicEvents(ctx, 1, 100)
	if err != nil || len(loadedEvents) != 1 || loadedEvents[0].EventID != e.ID || len(loadedEvents[0].TopicIDs) != 1 {
		t.Fatalf("restored events = %+v err=%v", loadedEvents, err)
	}
	batch, err := s.SummaryBatch(ctx, 50)
	if err != nil || len(batch.Events) != 1 || batch.Events[0].EventID != e.ID || batch.GroupID != 1 {
		t.Fatalf("summary batch = %+v err=%v", batch, err)
	}
	summary := conversation.TopicSummary{GroupID: 1, TopicID: "topic:1:1", Revision: 2, Summary: "讨论周末爬山", EvidenceIDs: []string{e.ID}, SourceSeq: e.Seq, UpdatedAt: time.Now()}
	if err := s.SaveTopicSummaries(ctx, 1, []conversation.TopicSummary{summary}, e.Seq); err != nil {
		t.Fatal(err)
	}
	batch, err = s.SummaryBatch(ctx, 50)
	if err != nil || len(batch.Events) != 0 {
		t.Fatalf("summary cursor did not advance: %+v err=%v", batch, err)
	}
	loadedViews, err = s.LoadTopics(ctx, 1)
	if err != nil || len(loadedViews) != 1 || loadedViews[0].Summary != "讨论周末爬山" {
		t.Fatalf("summary was not attached: %+v err=%v", loadedViews, err)
	}
}

func TestStickerRevisionChangesWithMaterialState(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := meme.Asset{ID: "revision-a", SourceID: "e1", SourceGroup: 1, URL: "https://example.com/a", State: "pending", LastSeen: time.Now()}
	if err := s.StageSticker(ctx, a); err != nil {
		t.Fatal(err)
	}
	before, err := s.StickerRevision(ctx)
	if err != nil || before < 1 {
		t.Fatalf("initial revision = %d err=%v", before, err)
	}
	if err := s.DisableSticker(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	after, err := s.StickerRevision(ctx)
	if err != nil || after <= before {
		t.Fatalf("material change did not bump revision: before=%d after=%d err=%v", before, after, err)
	}
}

func TestSummaryBatchDoesNotSplitSameSeqAssignments(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	first := event(t, s, "ambiguous", 1, 10, "爬山还是火锅")
	second := event(t, s, "later", 1, 10, "火锅也行")
	views := []conversation.TopicView{
		{ID: "topic:1:1", Participants: []int64{10}, FirstSeq: first.Seq, LastSeq: first.Seq, Revision: 1, LastActiveAt: time.Now(), Status: "active"},
		{ID: "topic:1:2", Participants: []int64{10}, FirstSeq: first.Seq, LastSeq: second.Seq, Revision: 1, LastActiveAt: time.Now(), Status: "active"},
	}
	events := []conversation.TopicEventView{
		{TopicID: "topic:1:1", EventID: first.ID, MessageID: first.MessageID, Seq: first.Seq, AssignedAt: time.Now(), TopicIDs: []string{"topic:1:1", "topic:1:2"}},
		{TopicID: "topic:1:2", EventID: second.ID, MessageID: second.MessageID, Seq: second.Seq, AssignedAt: time.Now(), TopicIDs: []string{"topic:1:2"}},
	}
	if err := s.SaveTopics(ctx, 1, views, events); err != nil {
		t.Fatal(err)
	}
	// A one-event page must return both ownership rows of the ambiguous message.
	batch, err := s.SummaryBatch(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Events) != 2 {
		t.Fatalf("page split same-seq assignments: %+v", batch.Events)
	}
	topics := map[string]bool{}
	for _, event := range batch.Events {
		if event.Seq != first.Seq {
			t.Fatalf("unexpected seq in first page: %+v", batch.Events)
		}
		topics[event.TopicID] = true
	}
	if !topics["topic:1:1"] || !topics["topic:1:2"] {
		t.Fatalf("missing ownership row: %+v", topics)
	}
	// After the summarizer commits the first seq, the next page continues.
	if err := s.SaveTopicSummaries(ctx, 1, nil, first.Seq); err != nil {
		t.Fatal(err)
	}
	batch, err = s.SummaryBatch(ctx, 1)
	if err != nil || len(batch.Events) != 1 || batch.Events[0].EventID != second.ID {
		t.Fatalf("next page lost events: %+v err=%v", batch.Events, err)
	}
}
