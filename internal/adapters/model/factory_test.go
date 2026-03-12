package model

import (
	"context"
	"errors"
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

func TestFactoryBuildsOpenAICompatibleModelWhenExplicitlyConfigured(t *testing.T) {
	factory := NewFactory(config.ModelsConfig{
		Main: config.ModelProviderConfig{
			Provider: "openai_compat",
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
