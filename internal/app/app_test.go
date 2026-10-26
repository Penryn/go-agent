package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/config"
)

func TestBootstrapHTTPAndPreviewNeverSendsQQ(t *testing.T) {
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
	cfg := config.Default()
	cfg.Storage.DatabaseURL = u.String()
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
