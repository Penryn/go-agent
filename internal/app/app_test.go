package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/chat"
	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/qq"
	"github.com/phlin/go-agent/internal/store"
)

// createTempDatabase boots a throwaway database and returns its DSN.
func createTempDatabase(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	required := dsn != ""
	if dsn == "" {
		dsn = "postgres://qqbot:qqbotpass@127.0.0.1:5432/postgres?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	admin, err := sql.Open("pgx", dsn)
	if err == nil {
		err = admin.PingContext(ctx)
	}
	cancel()
	if err != nil {
		if admin != nil {
			admin.Close()
		}
		if required {
			t.Fatal(err)
		}
		t.Skipf("PostgreSQL unavailable: %v", err)
	}
	name := fmt.Sprintf("friend_app_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(context.Background(), "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close()
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

func TestBootstrapHTTPAndPreviewNeverSendsQQ(t *testing.T) {
	cfg := config.Default()
	cfg.Storage.DatabaseURL = createTempDatabase(t)
	cfg.Models.Embedding.Dimensions = 3
	cfg.Meme.Path = t.TempDir()
	cfg.Chat.Merge = time.Millisecond
	cfg.Chat.MaxWait = 3 * time.Millisecond
	a, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.runtime.Sender != nil {
		t.Fatal("QQ disabled but sender attached")
	}
	server := httptest.NewServer(a.routes())
	defer server.Close()
	response, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	outcome, err := a.runtime.Process(context.Background(), []byte(`{"post_type":"message","message_type":"group","self_id":99,"group_id":1,"user_id":10,"message_id":1,"message":[{"type":"text","data":{"text":"hello"}},{"type":"at","data":{"qq":"99"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State == "confirmed" {
		t.Fatal("unconfigured model created a delivered message")
	}
	response, err = http.Get(server.URL + "/admin/api/conversation?group=1")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body struct {
		Messages []struct {
			Outbound bool `json:"outbound"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Messages) != 1 || body.Messages[0].Outbound {
		t.Fatal("preview produced phantom dialogue", body)
	}
	response, err = http.Get(server.URL + "/admin/api/mcp")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal("legacy tool API remains reachable")
	}
}

func TestOwnedSenderRunsQuotePreconditionsBeforeSend(t *testing.T) {
	dsn := createTempDatabase(t)
	db, err := store.Open(context.Background(), dsn, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.AcquireSender(context.Background()); err != nil {
		t.Fatal(err)
	}

	// quoteBot serves the three platform actions and records their order;
	// markReply controls the mark_msg_as_read outcome.
	quoteBot := func(t *testing.T, markReply func(w http.ResponseWriter)) (string, chan string) {
		actions := make(chan string, 8)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			actions <- r.URL.Path
			switch r.URL.Path {
			case "/get_msg":
				_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"message_id":55,"message_type":"group","group_id":1}}`))
			case "/mark_msg_as_read":
				markReply(w)
			case "/send_group_msg":
				_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"message_id":77}}`))
			default:
				t.Error("unexpected platform action", r.URL.Path)
			}
		}))
		t.Cleanup(server.Close)
		return server.URL, actions
	}
	ok := func(w http.ResponseWriter) { _, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":null}`)) }

	t.Run("quoted send order", func(t *testing.T) {
		url, actions := quoteBot(t, ok)
		receipt, err := ownedSender{&qq.Client{URL: url}, db}.Send(context.Background(), 1, "hello", "55", "")
		if err != nil || receipt.MessageID != "77" {
			t.Fatal(receipt, err)
		}
		if got := drain(actions); !slices.Equal(got, []string{"/get_msg", "/mark_msg_as_read", "/send_group_msg"}) {
			t.Fatal("unexpected platform action order", got)
		}
	})
	t.Run("read rejection blocks send", func(t *testing.T) {
		url, actions := quoteBot(t, func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"status":"failed","retcode":1200}`))
		})
		_, err := ownedSender{&qq.Client{URL: url}, db}.Send(context.Background(), 1, "hello", "55", "")
		var sendErr *qq.SendError
		if !errors.As(err, &sendErr) || !sendErr.Definite {
			t.Fatal("read rejection must be a definite precondition failure", err)
		}
		if got := drain(actions); !slices.Equal(got, []string{"/get_msg", "/mark_msg_as_read"}) {
			t.Fatal("send ran after a failed read precondition", got)
		}
	})
	t.Run("read timeout blocks send without retry", func(t *testing.T) {
		url, actions := quoteBot(t, func(w http.ResponseWriter) {
			time.Sleep(time.Second)
			_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":null}`))
		})
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		_, err := ownedSender{&qq.Client{URL: url}, db}.Send(ctx, 1, "hello", "55", "")
		var sendErr *qq.SendError
		if !errors.As(err, &sendErr) || sendErr.Definite {
			t.Fatal("read timeout must stay an uncertain failure", err)
		}
		if got := drain(actions); !slices.Equal(got, []string{"/get_msg", "/mark_msg_as_read"}) {
			t.Fatal("send retried or ran after an uncertain read", got)
		}
	})
	t.Run("unquoted send skips preconditions", func(t *testing.T) {
		url, actions := quoteBot(t, ok)
		sender := ownedSender{&qq.Client{URL: url}, db}
		if _, err := sender.Send(context.Background(), 1, "hello", "", ""); err != nil {
			t.Fatal(err)
		}
		if got := drain(actions); !slices.Equal(got, []string{"/send_group_msg"}) {
			t.Fatal("unquoted send ran quote preconditions", got)
		}
	})
	t.Run("lost lease blocks all platform actions", func(t *testing.T) {
		url, actions := quoteBot(t, ok)
		other, err := store.Open(context.Background(), dsn, 3)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		_, err = ownedSender{&qq.Client{URL: url}, other}.Send(context.Background(), 1, "hello", "55", "")
		var sendErr *qq.SendError
		if !errors.As(err, &sendErr) || !sendErr.Definite {
			t.Fatal("missing lease must be a definite failure", err)
		}
		if got := drain(actions); len(got) != 0 {
			t.Fatal("platform actions ran without send ownership", got)
		}
	})
}

func drain(actions chan string) []string {
	var got []string
	for {
		select {
		case a := <-actions:
			got = append(got, a)
		default:
			return got
		}
	}
}

// actionBot serves every platform action and records their order; failPath
// makes that action return an explicit platform rejection.
func actionBot(t *testing.T, failPath string) (string, chan string) {
	t.Helper()
	actions := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actions <- r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == failPath {
			_, _ = w.Write([]byte(`{"status":"failed","retcode":1200,"message":"denied"}`))
			return
		}
		switch r.URL.Path {
		case "/get_msg":
			_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"message_id":55,"message_type":"group","group_id":1}}`))
		case "/mark_msg_as_read", "/set_msg_emoji_like", "/get_group_member_info", "/group_poke", "/send_group_msg":
			_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":null}`))
		default:
			t.Error("unexpected platform action", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL, actions
}

func TestOwnedSenderReactionAndPokeBoundaries(t *testing.T) {
	dsn := createTempDatabase(t)
	db, err := store.Open(context.Background(), dsn, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.AcquireSender(context.Background()); err != nil {
		t.Fatal(err)
	}

	t.Run("reaction order", func(t *testing.T) {
		url, actions := actionBot(t, "")
		sender := ownedSender{&qq.Client{URL: url}, db}
		if err := sender.React(context.Background(), 1, "55", "76"); err != nil {
			t.Fatal(err)
		}
		if got := drain(actions); !slices.Equal(got, []string{"/get_msg", "/mark_msg_as_read", "/set_msg_emoji_like"}) {
			t.Fatal("unexpected reaction order", got)
		}
	})
	t.Run("read failure blocks reaction", func(t *testing.T) {
		url, actions := actionBot(t, "/mark_msg_as_read")
		sender := ownedSender{&qq.Client{URL: url}, db}
		err := sender.React(context.Background(), 1, "55", "76")
		var sendErr *qq.SendError
		if !errors.As(err, &sendErr) || !sendErr.Definite {
			t.Fatal("read rejection must be a definite precondition failure", err)
		}
		if got := drain(actions); !slices.Equal(got, []string{"/get_msg", "/mark_msg_as_read"}) {
			t.Fatal("reaction ran after a failed read precondition", got)
		}
	})
	t.Run("poke order", func(t *testing.T) {
		url, actions := actionBot(t, "")
		sender := ownedSender{&qq.Client{URL: url}, db}
		if err := sender.Poke(context.Background(), 1, 10); err != nil {
			t.Fatal(err)
		}
		if got := drain(actions); !slices.Equal(got, []string{"/get_group_member_info", "/group_poke"}) {
			t.Fatal("unexpected poke order", got)
		}
	})
	t.Run("membership failure blocks poke", func(t *testing.T) {
		url, actions := actionBot(t, "/get_group_member_info")
		sender := ownedSender{&qq.Client{URL: url}, db}
		err := sender.Poke(context.Background(), 1, 10)
		var sendErr *qq.SendError
		if !errors.As(err, &sendErr) || !sendErr.Definite {
			t.Fatal("membership rejection must be a definite precondition failure", err)
		}
		if got := drain(actions); !slices.Equal(got, []string{"/get_group_member_info"}) {
			t.Fatal("poke ran without membership confirmation", got)
		}
	})
	t.Run("lost lease blocks actions", func(t *testing.T) {
		url, actions := actionBot(t, "")
		other, err := store.Open(context.Background(), dsn, 3)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		sender := ownedSender{&qq.Client{URL: url}, other}
		for _, err := range []error{
			sender.React(context.Background(), 1, "55", "76"),
			sender.Poke(context.Background(), 1, 10),
		} {
			var sendErr *qq.SendError
			if !errors.As(err, &sendErr) || !sendErr.Definite {
				t.Fatal("missing lease must be a definite failure", err)
			}
		}
		if got := drain(actions); len(got) != 0 {
			t.Fatal("platform actions ran without send ownership", got)
		}
	})
}

func TestReconcilerClosesUnknownSends(t *testing.T) {
	dsn := createTempDatabase(t)
	db, err := store.Open(context.Background(), dsn, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.AcquireSender(context.Background()); err != nil {
		t.Fatal(err)
	}
	var queries atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries.Add(1)
		if r.URL.Path != "/get_msg" {
			t.Error("unexpected platform action during reconcile", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"message_id":77,"message_type":"group","group_id":1,"user_id":99}}`))
	}))
	t.Cleanup(server.Close)
	runtime := chat.New(context.Background(), &chat.Runtime{})
	t.Cleanup(runtime.Close)
	rec := &reconciler{qq: &qq.Client{URL: server.URL}, store: db, runtime: runtime, self: 99}
	ctx := context.Background()

	seed := func(id string, group int64, messageID string, reply chat.Reply) chat.Attempt {
		t.Helper()
		attempt := chat.Attempt{ID: id, GroupID: group, State: "planning", At: time.Now(), Reply: reply}
		if ok, err := db.BeginAttempt(ctx, attempt); err != nil || !ok {
			t.Fatal(ok, err)
		}
		attempt.State = "sending"
		if err := db.UpdateAttempt(ctx, attempt); err != nil {
			t.Fatal(err)
		}
		if err := db.SaveDelivery(ctx, attempt.ID, messageID, "message"); err != nil {
			t.Fatal(err)
		}
		attempt.State = "unknown"
		attempt.Error = "delivery confirmed but archive failed"
		if err := db.UpdateAttempt(ctx, attempt); err != nil {
			t.Fatal(err)
		}
		return attempt
	}

	t.Run("unique match archives once", func(t *testing.T) {
		attempt := seed("turn:1:1", 1, "77", chat.Reply{Action: "text", Text: "记住了"})
		if err := rec.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		var state, reconcileState string
		if err := db.DB.QueryRowContext(ctx, `SELECT state,reconcile_state FROM groupfriend.reply_attempts WHERE id=$1`, attempt.ID).Scan(&state, &reconcileState); err != nil {
			t.Fatal(err)
		}
		if state != "confirmed" || reconcileState != "resolved" {
			t.Fatal("reconciler did not close the attempt", state, reconcileState)
		}
		reply, err := db.IsBotReply(ctx, 1, "77")
		if err != nil || !reply {
			t.Fatal("reconciled message invisible to repliesToBot", reply, err)
		}
		// The resolved attempt is not picked up again; no extra query ran.
		before := queries.Load()
		if err := rec.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		if queries.Load() != before {
			t.Fatal("resolved attempt was queried again")
		}
	})
	t.Run("foreign sender is unresolvable", func(t *testing.T) {
		foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"message_id":78,"message_type":"group","group_id":1,"user_id":10}}`))
		}))
		t.Cleanup(foreign.Close)
		bot := &reconciler{qq: &qq.Client{URL: foreign.URL}, store: db, runtime: runtime, self: 99}
		attempt := seed("turn:1:2", 1, "78", chat.Reply{Action: "text", Text: "不是我的"})
		if err := bot.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		var reconcileState string
		if err := db.DB.QueryRowContext(ctx, `SELECT reconcile_state FROM groupfriend.reply_attempts WHERE id=$1`, attempt.ID).Scan(&reconcileState); err != nil {
			t.Fatal(err)
		}
		if reconcileState != "unresolvable" {
			t.Fatal("foreign message should be unresolvable", reconcileState)
		}
		if reply, err := db.IsBotReply(ctx, 1, "78"); err == nil && reply {
			t.Fatal("foreign sender archived as bot reply")
		}
	})
	t.Run("uncertain answer defers without closing", func(t *testing.T) {
		flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("gateway failed"))
		}))
		t.Cleanup(flaky.Close)
		bot := &reconciler{qq: &qq.Client{URL: flaky.URL}, store: db, runtime: runtime, self: 99}
		attempt := seed("turn:1:3", 1, "79", chat.Reply{Action: "text", Text: "不确定"})
		if err := bot.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		var state, reconcileState string
		var due sql.NullTime
		if err := db.DB.QueryRowContext(ctx, `SELECT state,reconcile_state,reconcile_at FROM groupfriend.reply_attempts WHERE id=$1`, attempt.ID).Scan(&state, &reconcileState, &due); err != nil {
			t.Fatal(err)
		}
		if state != "unknown" || !due.Valid || !due.Time.After(time.Now()) {
			t.Fatal("uncertain reconcile closed or did not back off", state, reconcileState, due)
		}
		dueAttempts, err := db.ReconcileDue(ctx, 10)
		if err != nil || len(dueAttempts) != 0 {
			t.Fatal("deferred candidate surfaced too early", dueAttempts, err)
		}
	})
	t.Run("action retry never calls the platform", func(t *testing.T) {
		attempt := seed("turn:2:1", 2, "80", chat.Reply{Action: "reaction", Quote: "55", EmojiID: "76"})
		if err := db.MarkReconcile(ctx, attempt.ID, "action confirmed but archive failed"); err != nil {
			t.Fatal(err)
		}
		before := queries.Load()
		if err := rec.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		if queries.Load() != before {
			t.Fatal("action reconciliation touched the platform API")
		}
		var state string
		if err := db.DB.QueryRowContext(ctx, `SELECT state FROM groupfriend.reply_attempts WHERE id=$1`, attempt.ID).Scan(&state); err != nil || state != "confirmed" {
			t.Fatal("action attempt not confirmed by retry", state, err)
		}
		var actions int
		if err := db.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM groupfriend.conversation_topic_actions WHERE attempt_id=$1`, attempt.ID).Scan(&actions); err != nil || actions != 0 {
			t.Fatal("topic action recorded without a topic", actions, err)
		}
	})
}
