package model

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/phlin/go-agent/internal/config"
)

func TestFactoryBuildsArkModelByDefault(t *testing.T) {
	factory := NewFactory(config.ModelsConfig{
		Main: config.ModelProviderConfig{
			Provider: "",
			Model:    "ep-202603130001-demo",
			APIKey:   "test-key",
			Timeout:  "2s",
		},
	})

	model, err := factory.MainChatModel(context.Background())
	if err != nil {
		t.Fatalf("main chat model: %v", err)
	}
	if model == nil {
		t.Fatal("expected non-nil model")
	}

	second, err := factory.MainChatModel(context.Background())
	if err != nil {
		t.Fatalf("cached main chat model: %v", err)
	}
	if second != model {
		t.Fatal("expected cached model instance")
	}
}

func TestFactoryBuildsOpenAIModel(t *testing.T) {
	factory := NewFactory(config.ModelsConfig{
		Main: config.ModelProviderConfig{
			Provider: "openai",
			Model:    "gpt-4o-mini",
			BaseURL:  "http://127.0.0.1:18080/v1",
			APIKey:   "test-key",
			Timeout:  "2s",
		},
	})

	model, err := factory.MainChatModel(context.Background())
	if err != nil {
		t.Fatalf("main chat model: %v", err)
	}
	if model == nil {
		t.Fatal("expected non-nil model")
	}
}

func TestFactoryReturnsUnavailableWhenModelConfigMissing(t *testing.T) {
	factory := NewFactory(config.ModelsConfig{})

	_, err := factory.MainChatModel(context.Background())
	if !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("expected ErrModelUnavailable, got %v", err)
	}
}

func TestFactoryWarmupFailsOnUnsupportedProvider(t *testing.T) {
	factory := NewFactory(config.ModelsConfig{
		Main: config.ModelProviderConfig{
			Provider: "unknown",
			Model:    "demo",
			APIKey:   "secret",
		},
	})

	err := factory.Warmup(context.Background())
	if err == nil {
		t.Fatal("expected warmup error")
	}
}

func TestFactoryDoesNotCacheErrors(t *testing.T) {
	factory := NewFactory(config.ModelsConfig{
		Main: config.ModelProviderConfig{
			Provider: "unknown",
			Model:    "demo",
			APIKey:   "secret",
		},
	})

	_, err1 := factory.MainChatModel(context.Background())
	if err1 == nil {
		t.Fatal("expected error on first call")
	}

	// Verify error is not cached by checking internal cache map
	factory.mu.Lock()
	_, cached := factory.cached["main"]
	factory.mu.Unlock()
	if cached {
		t.Fatal("error should not be cached")
	}
}

func TestFactoryBuildsArkEmbeddingModelByDefault(t *testing.T) {
	factory := NewFactory(config.ModelsConfig{
		Embedding: config.ModelProviderConfig{
			Provider: "",
			Model:    "ep-202603130002-emb",
			APIKey:   "test-key",
			Timeout:  "2s",
		},
	})

	embedder, err := factory.EmbeddingModel(context.Background())
	if err != nil {
		t.Fatalf("embedding model: %v", err)
	}
	if embedder == nil {
		t.Fatal("expected non-nil embedder")
	}

	second, err := factory.EmbeddingModel(context.Background())
	if err != nil {
		t.Fatalf("cached embedding model: %v", err)
	}
	if second != embedder {
		t.Fatal("expected cached embedder instance")
	}
}

func TestFactoryBuildsOpenAIEmbeddingModel(t *testing.T) {
	factory := NewFactory(config.ModelsConfig{
		Embedding: config.ModelProviderConfig{
			Provider: "openai",
			Model:    "text-embedding-3-small",
			BaseURL:  "http://127.0.0.1:18080/v1",
			APIKey:   "test-key",
			Timeout:  "2s",
		},
	})

	embedder, err := factory.EmbeddingModel(context.Background())
	if err != nil {
		t.Fatalf("embedding model: %v", err)
	}
	if embedder == nil {
		t.Fatal("expected non-nil embedder")
	}
}

func TestNormalizeProvider(t *testing.T) {
	for _, test := range []struct {
		provider string
		want     string
	}{
		{provider: "", want: "ark"},
		{provider: "ARK", want: "ark"},
		{provider: " openai ", want: "openai"},
	} {
		if got := normalizeProvider(test.provider); got != test.want {
			t.Fatalf("normalizeProvider(%q) = %q, want %q", test.provider, got, test.want)
		}
	}
}

func TestFactoryPassesEmbeddingDimensionsToArk(t *testing.T) {
	var request struct {
		Model      string `json:"model"`
		Dimensions int    `json:"dimensions"`
		Input      []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"input"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings/multimodal" {
			t.Errorf("unexpected request path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode embedding request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"probe","model":"embedding-endpoint","object":"list","data":{"object":"embedding","embedding":[0.1,0.2]},"usage":{"prompt_tokens":1,"total_tokens":1,"prompt_tokens_details":{"text_tokens":1,"image_tokens":0}}}`))
	}))
	defer server.Close()

	factory := NewFactory(config.ModelsConfig{Embedding: config.ModelProviderConfig{
		Provider:   "ark",
		Model:      "embedding-endpoint",
		BaseURL:    server.URL,
		APIKey:     "test-key",
		APIType:    "multimodal",
		Dimensions: 2048,
	}})
	embedder, err := factory.EmbeddingModel(context.Background())
	if err != nil {
		t.Fatalf("embedding model: %v", err)
	}
	if _, err := embedder.EmbedStrings(context.Background(), []string{"dimension probe"}); err != nil {
		t.Fatalf("embed probe: %v", err)
	}

	if request.Model != "embedding-endpoint" || request.Dimensions != 2048 {
		t.Fatalf("unexpected embedding request: %+v", request)
	}
	if len(request.Input) != 1 || request.Input[0].Type != "text" || request.Input[0].Text != "dimension probe" {
		t.Fatalf("unexpected embedding input: %+v", request.Input)
	}
}

func TestFactoryReturnsUnavailableWhenEmbeddingConfigMissing(t *testing.T) {
	factory := NewFactory(config.ModelsConfig{})

	_, err := factory.EmbeddingModel(context.Background())
	if !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("expected ErrModelUnavailable, got %v", err)
	}
}

func TestFactoryWarmupSkipsUnconfiguredEmbedding(t *testing.T) {
	factory := NewFactory(config.ModelsConfig{})

	err := factory.Warmup(context.Background())
	if err != nil {
		t.Fatalf("warmup should succeed with no models configured: %v", err)
	}
}

func TestFactoryWarmupFailsOnUnsupportedEmbeddingProvider(t *testing.T) {
	factory := NewFactory(config.ModelsConfig{
		Embedding: config.ModelProviderConfig{
			Provider: "unknown",
			Model:    "demo",
			APIKey:   "secret",
		},
	})

	err := factory.Warmup(context.Background())
	if err == nil {
		t.Fatal("expected warmup error for unsupported embedding provider")
	}
}
