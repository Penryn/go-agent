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
	"testing"
	"time"

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
