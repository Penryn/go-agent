package model

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	arkmodel "github.com/cloudwego/eino-ext/components/model/ark"
	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	modelcomponent "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/phlin/go-agent/internal/config"
)

var ErrModelUnavailable = errors.New("chat model unavailable")

type Factory struct {
	cfg    config.ModelsConfig
	mu     sync.Mutex
	cached map[string]cachedChatModel
}

func NewFactory(cfg config.ModelsConfig) *Factory {
	return &Factory{
		cfg:    cfg,
		cached: make(map[string]cachedChatModel, 3),
	}
}

func (f *Factory) MainChatModel(ctx context.Context) (modelcomponent.BaseChatModel, error) {
	return f.chatModel(ctx, "main", f.cfg.Main)
}

func (f *Factory) GateChatModel(ctx context.Context) (modelcomponent.BaseChatModel, error) {
	return f.chatModel(ctx, "gate", f.cfg.Gate)
}

func (f *Factory) VisionChatModel(ctx context.Context) (modelcomponent.BaseChatModel, error) {
	return f.chatModel(ctx, "vision", f.cfg.Vision)
}

func (f *Factory) Warmup(ctx context.Context) error {
	models := []struct {
		name string
		cfg  config.ModelProviderConfig
	}{
		{name: "main", cfg: f.cfg.Main},
		{name: "gate", cfg: f.cfg.Gate},
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
	return nil
}

func (f *Factory) chatModel(ctx context.Context, key string, cfg config.ModelProviderConfig) (modelcomponent.BaseChatModel, error) {
	f.mu.Lock()
	cached, ok := f.cached[key]
	f.mu.Unlock()
	if ok {
		return cached.model, cached.err
	}

	model, err := f.newChatModel(ctx, cfg)

	f.mu.Lock()
	f.cached[key] = cachedChatModel{model: model, err: err}
	f.mu.Unlock()
	return model, err
}

func (f *Factory) newChatModel(ctx context.Context, cfg config.ModelProviderConfig) (modelcomponent.BaseChatModel, error) {
	if strings.TrimSpace(cfg.APIKey) == "" || strings.TrimSpace(cfg.Model) == "" {
		return nil, ErrModelUnavailable
	}

	switch normalizeProvider(cfg.Provider) {
	case "ark":
		timeout := parseTimeout(cfg.Timeout, 30*time.Second)
		return arkmodel.NewChatModel(ctx, &arkmodel.ChatModelConfig{
			APIKey:  cfg.APIKey,
			Model:   cfg.Model,
			BaseURL: strings.TrimSpace(cfg.BaseURL),
			Timeout: durationPtr(timeout),
		})
	case "openai":
		timeout := parseTimeout(cfg.Timeout, 30*time.Second)
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

type cachedChatModel struct {
	model modelcomponent.BaseChatModel
	err   error
}

func modelConfigured(cfg config.ModelProviderConfig) bool {
	return strings.TrimSpace(cfg.Model) != "" ||
		strings.TrimSpace(cfg.APIKey) != "" ||
		strings.TrimSpace(cfg.BaseURL) != ""
}

func normalizeProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "ark":
		return "ark"
	case "openai", "openai_compat", "openai-compatible", "openai_compatible":
		return "openai"
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}

func parseTimeout(raw string, fallback time.Duration) time.Duration {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout <= 0 {
		return fallback
	}
	return timeout
}

func durationPtr(value time.Duration) *time.Duration {
	return &value
}

type StaticFactory struct {
	MainModel   modelcomponent.BaseChatModel
	GateModel   modelcomponent.BaseChatModel
	VisionModel modelcomponent.BaseChatModel
}

func (f StaticFactory) MainChatModel(_ context.Context) (modelcomponent.BaseChatModel, error) {
	if f.MainModel == nil {
		return nil, ErrModelUnavailable
	}
	return f.MainModel, nil
}

func (f StaticFactory) GateChatModel(_ context.Context) (modelcomponent.BaseChatModel, error) {
	if f.GateModel == nil {
		return nil, ErrModelUnavailable
	}
	return f.GateModel, nil
}

func (f StaticFactory) VisionChatModel(_ context.Context) (modelcomponent.BaseChatModel, error) {
	if f.VisionModel == nil {
		return nil, ErrModelUnavailable
	}
	return f.VisionModel, nil
}

type MockChatModel struct {
	mu        sync.Mutex
	responses []*schema.Message
	index     int
	inputs    [][]*schema.Message
}

func NewMockChatModel(responses ...*schema.Message) *MockChatModel {
	return &MockChatModel{responses: responses}
}

func (m *MockChatModel) Generate(_ context.Context, input []*schema.Message, _ ...modelcomponent.Option) (*schema.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(input) > 0 {
		m.inputs = append(m.inputs, append([]*schema.Message(nil), input...))
	}

	if len(m.responses) == 0 {
		return schema.AssistantMessage("", nil), nil
	}

	if m.index >= len(m.responses) {
		return m.responses[len(m.responses)-1], nil
	}

	response := m.responses[m.index]
	m.index++
	return response, nil
}

func (m *MockChatModel) Stream(ctx context.Context, messages []*schema.Message, opts ...modelcomponent.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *MockChatModel) Inputs() [][]*schema.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([][]*schema.Message(nil), m.inputs...)
}
