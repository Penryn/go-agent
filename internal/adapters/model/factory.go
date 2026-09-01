package model

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	arkemb "github.com/cloudwego/eino-ext/components/embedding/ark"
	openaiemb "github.com/cloudwego/eino-ext/components/embedding/openai"
	arkmodel "github.com/cloudwego/eino-ext/components/model/ark"
	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/embedding"
	modelcomponent "github.com/cloudwego/eino/components/model"
	arkruntime "github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"

	"github.com/phlin/go-agent/internal/application/textutil"
	"github.com/phlin/go-agent/internal/config"
)

var ErrModelUnavailable = errors.New("chat model unavailable")

type Factory struct {
	cfg             config.ModelsConfig
	mu              sync.Mutex
	cached          map[string]modelcomponent.BaseChatModel
	cachedEmbedders map[string]embedding.Embedder
}

func NewFactory(cfg config.ModelsConfig) *Factory {
	return &Factory{
		cfg:             cfg,
		cached:          make(map[string]modelcomponent.BaseChatModel, 3),
		cachedEmbedders: make(map[string]embedding.Embedder, 1),
	}
}

func (f *Factory) MainChatModel(ctx context.Context) (modelcomponent.BaseChatModel, error) {
	return f.chatModel(ctx, "main", f.cfg.Main)
}

func (f *Factory) VisionChatModel(ctx context.Context) (modelcomponent.BaseChatModel, error) {
	return f.chatModel(ctx, "vision", f.cfg.Vision)
}

func (f *Factory) EmbeddingModel(ctx context.Context) (embedding.Embedder, error) {
	return f.embeddingModel(ctx, "embedding", f.cfg.Embedding)
}

func (f *Factory) Warmup(ctx context.Context) error {
	models := []struct {
		name string
		cfg  config.ModelProviderConfig
	}{
		{name: "main", cfg: f.cfg.Main},
		{name: "vision", cfg: f.cfg.Vision},
	}

	for _, item := range models {
		if !modelConfigured(item.cfg) {
			continue
		}
		if _, err := f.chatModel(ctx, item.name, item.cfg); err != nil {
			return fmt.Errorf("initialize %s chat model: %w", item.name, err)
		}
	}

	if modelConfigured(f.cfg.Embedding) {
		if _, err := f.embeddingModel(ctx, "embedding", f.cfg.Embedding); err != nil {
			return fmt.Errorf("initialize embedding model: %w", err)
		}
	}

	return nil
}

func (f *Factory) chatModel(ctx context.Context, key string, cfg config.ModelProviderConfig) (modelcomponent.BaseChatModel, error) {
	f.mu.Lock()
	cached, ok := f.cached[key]
	f.mu.Unlock()
	if ok {
		return cached, nil
	}

	model, err := f.newChatModel(ctx, cfg)
	if err != nil {
		// Don't cache transient errors so the next call retries.
		// Config errors (ErrModelUnavailable, unsupported provider) are cheap to
		// re-evaluate, so skipping the cache for them is acceptable.
		return nil, err
	}

	f.mu.Lock()
	f.cached[key] = model
	f.mu.Unlock()
	return model, nil
}

func (f *Factory) newChatModel(ctx context.Context, cfg config.ModelProviderConfig) (modelcomponent.BaseChatModel, error) {
	if strings.TrimSpace(cfg.APIKey) == "" || strings.TrimSpace(cfg.Model) == "" {
		return nil, ErrModelUnavailable
	}

	switch normalizeProvider(cfg.Provider) {
	case "ark":
		timeout := textutil.ParseDurationOr(cfg.Timeout, 30*time.Second)
		return arkmodel.NewChatModel(ctx, &arkmodel.ChatModelConfig{
			APIKey:   cfg.APIKey,
			Model:    cfg.Model,
			BaseURL:  strings.TrimSpace(cfg.BaseURL),
			Timeout:  durationPtr(timeout),
			Thinking: &arkruntime.Thinking{Type: arkruntime.ThinkingTypeDisabled}, // 禁用深度思考，避免长时间等待
		})
	case "openai":
		timeout := textutil.ParseDurationOr(cfg.Timeout, 30*time.Second)
		return openaimodel.NewChatModel(ctx, &openaimodel.ChatModelConfig{
			APIKey:  cfg.APIKey,
			Model:   cfg.Model,
			BaseURL: strings.TrimSpace(cfg.BaseURL),
			HTTPClient: &http.Client{
				Timeout: timeout,
			},
		})
	default:
		return nil, fmt.Errorf("unsupported chat model provider %q", cfg.Provider)
	}
}

func (f *Factory) embeddingModel(ctx context.Context, key string, cfg config.ModelProviderConfig) (embedding.Embedder, error) {
	f.mu.Lock()
	cached, ok := f.cachedEmbedders[key]
	f.mu.Unlock()
	if ok {
		return cached, nil
	}

	model, err := f.newEmbeddingModel(ctx, cfg)
	if err != nil {
		return nil, err
	}

	f.mu.Lock()
	f.cachedEmbedders[key] = model
	f.mu.Unlock()
	return model, nil
}

func (f *Factory) newEmbeddingModel(ctx context.Context, cfg config.ModelProviderConfig) (embedding.Embedder, error) {
	if strings.TrimSpace(cfg.APIKey) == "" || strings.TrimSpace(cfg.Model) == "" {
		return nil, ErrModelUnavailable
	}

	switch normalizeProvider(cfg.Provider) {
	case "ark":
		timeout := textutil.ParseDurationOr(cfg.Timeout, 15*time.Second)
		embCfg := &arkemb.EmbeddingConfig{
			APIKey:  cfg.APIKey,
			Model:   cfg.Model,
			BaseURL: strings.TrimSpace(cfg.BaseURL),
			Timeout: durationPtr(timeout),
		}
		if cfg.Dimensions > 0 {
			dimensions := cfg.Dimensions
			embCfg.Dimensions = &dimensions
		}
		if strings.ToLower(strings.TrimSpace(cfg.APIType)) == "multimodal" {
			apiType := arkemb.APITypeMultiModal
			embCfg.APIType = &apiType
		}
		return arkemb.NewEmbedder(ctx, embCfg)
	case "openai":
		timeout := textutil.ParseDurationOr(cfg.Timeout, 15*time.Second)
		embCfg := &openaiemb.EmbeddingConfig{
			APIKey:  cfg.APIKey,
			Model:   cfg.Model,
			BaseURL: strings.TrimSpace(cfg.BaseURL),
			HTTPClient: &http.Client{
				Timeout: timeout,
			},
		}
		if cfg.Dimensions > 0 {
			dimensions := cfg.Dimensions
			embCfg.Dimensions = &dimensions
		}
		return openaiemb.NewEmbedder(ctx, embCfg)
	default:
		return nil, fmt.Errorf("unsupported embedding model provider %q", cfg.Provider)
	}
}

func modelConfigured(cfg config.ModelProviderConfig) bool {
	return strings.TrimSpace(cfg.Model) != "" ||
		strings.TrimSpace(cfg.APIKey) != "" ||
		strings.TrimSpace(cfg.BaseURL) != ""
}

func normalizeProvider(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return "ark"
	}
	return provider
}

func durationPtr(value time.Duration) *time.Duration {
	return &value
}
