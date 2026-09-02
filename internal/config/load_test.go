package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	personadomain "github.com/phlin/go-agent/internal/domain/persona"
)

func TestLoadDoesNotOverrideYAMLSecretsFromEnvironment(t *testing.T) {
	t.Setenv("QQBOT_QQ_OUTBOUND_TOKEN", "env-http-secret")
	t.Setenv("QQBOT_MAIN_MODEL_API_KEY", "env-model-secret")
	t.Setenv("QQBOT_STORAGE_POSTGRES_PASSWORD", "env-db-secret")

	dir := t.TempDir()

	path := filepath.Join(dir, "config.yaml")
	content := "app:\n  mode: test\nmodels:\n  main:\n    api_key: model-secret\npersona:\n  id: test\n  name: Test Bot\nstorage:\n  postgres:\n    password: db-secret\nqq:\n  outbound_token: http-secret\n  event_ws_token: ws-secret\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got, want := cfg.QQ.OutboundToken, "http-secret"; got != want {
		t.Fatalf("qq outbound token mismatch: got %q want %q", got, want)
	}
	if got, want := cfg.QQ.EventWSToken, "ws-secret"; got != want {
		t.Fatalf("qq event websocket token mismatch: got %q want %q", got, want)
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
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load example config: %v", err)
	}
	if cfg.Persona.Name != "芙芙" || len(cfg.Persona.ResponseScenarios) == 0 {
		t.Fatalf("example persona runtime config not loaded: %+v", cfg.Persona)
	}
	seenGoal := false
	for _, fact := range cfg.Persona.Facts {
		seenGoal = seenGoal || fact.Key == "goal.current"
	}
	if !seenGoal {
		t.Fatalf("example persona current goal not loaded: %+v", cfg.Persona.Facts)
	}
}

func TestValidateRejectsInvalidPersonaFactTimestamp(t *testing.T) {
	cfg := Default()
	cfg.Persona.InitialFacts = []personadomain.PersonaFactSeed{{Key: "school_status", Value: "test", EffectiveAt: "tomorrow"}}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected invalid persona fact timestamp to fail validation")
	}
}

func TestDefaultToolAllowlistEmpty(t *testing.T) {
	cfg := Default()
	if cfg.DefaultPolicy.ToolAllowlist != nil {
		t.Fatalf("expected nil tool_allowlist by default, got %v", cfg.DefaultPolicy.ToolAllowlist)
	}
}

func TestDefaultPersonaIncludesConversationalVoiceExamples(t *testing.T) {
	cfg := Default()
	if !strings.Contains(cfg.Persona.SpeechStyle, "句长随场景变化") {
		t.Fatalf("default persona is missing variable conversational rhythm: %q", cfg.Persona.SpeechStyle)
	}
	if len(cfg.Persona.Speech.FewShotExamples) < 4 {
		t.Fatalf("default persona needs enough voice examples, got %d", len(cfg.Persona.Speech.FewShotExamples))
	}
	for _, example := range cfg.Persona.Speech.FewShotExamples {
		if strings.TrimSpace(example.UserSays) == "" || strings.TrimSpace(example.BotSays) == "" {
			t.Fatalf("default persona contains an empty voice example: %+v", example)
		}
	}
	if cfg.Persona.Speech.EmojiFrequency != "none" {
		t.Fatalf("default persona should avoid text emoji, got %q", cfg.Persona.Speech.EmojiFrequency)
	}
	if !cfg.Persona.PreferMemes {
		t.Fatal("default persona should allow meme images independently of text emoji")
	}
}

func TestDefaultGroupWhitelistEmpty(t *testing.T) {
	cfg := Default()
	if cfg.QQ.GroupWhitelist != nil {
		t.Fatalf("expected nil group_whitelist by default, got %v", cfg.QQ.GroupWhitelist)
	}
}

func TestDefaultEmbeddingDimensionsMatchVectorSchema(t *testing.T) {
	cfg := Default()
	if got, want := cfg.Models.Embedding.Dimensions, cfg.Storage.Postgres.VectorDim; got != want {
		t.Fatalf("embedding dimensions mismatch: got %d want %d", got, want)
	}
}

func TestValidateRejectsEmbeddingDimensionMismatch(t *testing.T) {
	cfg := Default()
	cfg.Models.Embedding.Model = "embedding-endpoint"
	cfg.Models.Embedding.APIKey = "test-key"
	cfg.Models.Embedding.Dimensions = 1024

	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "models.embedding.dimensions must match") {
		t.Fatalf("expected embedding dimension mismatch, got %v", err)
	}
}

func TestValidateAllowsDisabledVectorStore(t *testing.T) {
	cfg := Default()
	cfg.Storage.Postgres.VectorDim = 0
	cfg.Models.Embedding.Model = "embedding-endpoint"
	cfg.Models.Embedding.APIKey = "test-key"

	if err := Validate(cfg); err != nil {
		t.Fatalf("disabled vector store should not require embedding dimension match: %v", err)
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
