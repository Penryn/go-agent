package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReadsDotEnvSecrets(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("app:\n  mode: test\npersona:\n  id: test\n  name: Test Bot\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("QQBOT_QQ_ACCESS_TOKEN=qq-secret\nQQBOT_MAIN_MODEL_API_KEY=model-secret\nQQBOT_STORAGE_MYSQL_PASSWORD=db-secret\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got, want := cfg.QQ.AccessToken, "qq-secret"; got != want {
		t.Fatalf("qq access token mismatch: got %q want %q", got, want)
	}
	if got, want := cfg.Models.Main.APIKey, "model-secret"; got != want {
		t.Fatalf("main model api key mismatch: got %q want %q", got, want)
	}
	if got, want := cfg.Storage.MySQL.Password, "db-secret"; got != want {
		t.Fatalf("mysql password mismatch: got %q want %q", got, want)
	}
}

func TestLoadIgnoresPublicEnvOverride(t *testing.T) {
	t.Setenv("QQBOT_SERVER_HTTP_LISTEN", ":9090")
	t.Setenv("QQBOT_QQ_OUTBOUND_URL", "http://127.0.0.1:9999")

	dir := t.TempDir()
	t.Chdir(dir)

	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  http_listen: \":8081\"\npersona:\n  id: test\n  name: Test Bot\nqq:\n  outbound_url: http://127.0.0.1:3000\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got, want := cfg.Server.HTTPListen, ":8081"; got != want {
		t.Fatalf("http listen mismatch: got %q want %q", got, want)
	}
	if got, want := cfg.QQ.OutboundURL, "http://127.0.0.1:3000"; got != want {
		t.Fatalf("qq outbound url mismatch: got %q want %q", got, want)
	}
}

func TestDefaultToolAllowlistEmpty(t *testing.T) {
	cfg := Default()
	if cfg.DefaultPolicy.ToolAllowlist != nil {
		t.Fatalf("expected nil tool_allowlist by default, got %v", cfg.DefaultPolicy.ToolAllowlist)
	}
}

func TestDefaultLLMGateEnabled(t *testing.T) {
	cfg := Default()
	if !cfg.Autonomy.LLMGateEnabled {
		t.Fatal("expected LLMGateEnabled=true by default")
	}
}

func TestDefaultGroupWhitelistEmpty(t *testing.T) {
	cfg := Default()
	if cfg.QQ.GroupWhitelist != nil {
		t.Fatalf("expected nil group_whitelist by default, got %v", cfg.QQ.GroupWhitelist)
	}
}

func TestValidateRequiresQQEventWSURLWhenQQEnabled(t *testing.T) {
	cfg := Default()
	cfg.Persona.ID = "test"
	cfg.Persona.Name = "Test Bot"
	cfg.QQ.Enabled = true
	cfg.QQ.OutboundURL = "http://127.0.0.1:3000"
	cfg.QQ.EventWSURL = ""

	err := Validate(cfg)
	if err == nil || err.Error() != "qq.event_ws_url is required when qq.enabled=true" {
		t.Fatalf("unexpected validate error: %v", err)
	}
}

func TestValidateRequiresQQSelfIDWhenQQEnabled(t *testing.T) {
	cfg := Default()
	cfg.Persona.ID = "test"
	cfg.Persona.Name = "Test Bot"
	cfg.QQ.Enabled = true
	cfg.QQ.OutboundURL = "http://127.0.0.1:3000"
	cfg.QQ.EventWSURL = "ws://127.0.0.1:3001/event"
	cfg.QQ.SelfID = 0

	err := Validate(cfg)
	if err == nil || err.Error() != "qq.self_id must be a positive QQ number when qq.enabled=true" {
		t.Fatalf("unexpected validate error: %v", err)
	}
}
