package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDoesNotOverrideYAMLSecretsFromEnvironment(t *testing.T) {
	t.Setenv("QQBOT_QQ_ACCESS_TOKEN", "env-qq-secret")
	t.Setenv("QQBOT_MAIN_MODEL_API_KEY", "env-model-secret")
	t.Setenv("QQBOT_STORAGE_POSTGRES_PASSWORD", "env-db-secret")

	dir := t.TempDir()

	path := filepath.Join(dir, "config.yaml")
	content := "app:\n  mode: test\nmodels:\n  main:\n    api_key: model-secret\npersona:\n  id: test\n  name: Test Bot\nstorage:\n  postgres:\n    password: db-secret\nqq:\n  access_token: qq-secret\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
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
	if got, want := cfg.Storage.Postgres.Password, "db-secret"; got != want {
		t.Fatalf("postgres password mismatch: got %q want %q", got, want)
	}
}

func TestLoadReadsPublicYAMLConfig(t *testing.T) {
	dir := t.TempDir()

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

func TestExampleConfigLoads(t *testing.T) {
	path := filepath.Join("..", "..", "configs", "config.example.yaml")
	if _, err := Load(path); err != nil {
		t.Fatalf("load example config: %v", err)
	}
}

func TestDefaultToolAllowlistEmpty(t *testing.T) {
	cfg := Default()
	if cfg.DefaultPolicy.ToolAllowlist != nil {
		t.Fatalf("expected nil tool_allowlist by default, got %v", cfg.DefaultPolicy.ToolAllowlist)
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
