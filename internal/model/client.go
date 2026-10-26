package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	arkemb "github.com/cloudwego/eino-ext/components/embedding/ark"
	openaiemb "github.com/cloudwego/eino-ext/components/embedding/openai"
	arkmodel "github.com/cloudwego/eino-ext/components/model/ark"
	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/embedding"
	cm "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	arkruntime "github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
)

type Provider struct {
	Provider   string        `yaml:"provider"`
	Model      string        `yaml:"model"`
	BaseURL    string        `yaml:"base_url"`
	APIKey     string        `yaml:"api_key"`
	Timeout    time.Duration `yaml:"timeout"`
	APIType    string        `yaml:"api_type"`
	Dimensions int           `yaml:"dimensions"`
}
type Config struct {
	Main      Provider `yaml:"main"`
	Vision    Provider `yaml:"vision"`
	Embedding Provider `yaml:"embedding"`
}
type Usage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}
type Result struct {
	Content string
	Usage   Usage
}
type Generator interface {
	Generate(context.Context, string, any, []string) (Result, error)
}
type Embedder interface {
	Embed(context.Context, string) ([]float32, error)
	EmbeddingVersion() string
}

var ErrUnavailable = errors.New("model not configured")

type backgroundKey struct{}

func Background(ctx context.Context) context.Context {
	return context.WithValue(ctx, backgroundKey{}, true)
}

type Client struct {
	cfg          Config
	mu           sync.Mutex
	main, vision cm.BaseChatModel
	embedding    embedding.Embedder
	slots        chan struct{}
	background   chan struct{}
	waiting      atomic.Int64
}

func New(cfg Config, concurrency int) *Client {
	return &Client{cfg: cfg, slots: make(chan struct{}, max(1, concurrency)), background: make(chan struct{}, 1)}
}
func (c *Client) acquire(ctx context.Context) (func(), error) {
	bg, _ := ctx.Value(backgroundKey{}).(bool)
	if bg {
		select {
		case c.background <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		for c.waiting.Load() > 0 {
			timer := time.NewTimer(10 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				<-c.background
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	} else {
		c.waiting.Add(1)
		defer c.waiting.Add(-1)
	}
	select {
	case c.slots <- struct{}{}:
		return func() {
			<-c.slots
			if bg {
				<-c.background
			}
		}, nil
	case <-ctx.Done():
		if bg {
			<-c.background
		}
		return nil, ctx.Err()
	}
}
func (c *Client) Generate(ctx context.Context, system string, input any, images []string) (Result, error) {
	release, err := c.acquire(ctx)
	if err != nil {
		return Result{}, err
	}
	defer release()
	c.mu.Lock()
	m := c.main
	cfg := c.cfg.Main
	vision := len(images) > 0 && c.cfg.Vision.Model != ""
	if vision {
		m = c.vision
		cfg = c.cfg.Vision
	}
	if m == nil {
		m, err = newChat(ctx, cfg)
		if err == nil {
			if vision {
				c.vision = m
			} else {
				c.main = m
			}
		}
	}
	c.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	data, err := json.Marshal(input)
	if err != nil {
		return Result{}, err
	}
	user := schema.UserMessage(string(data))
	if len(images) > 0 {
		user.UserInputMultiContent = []schema.MessageInputPart{{Type: schema.ChatMessagePartTypeText, Text: string(data)}}
		user.Content = ""
		for _, u := range images {
			url := u
			user.UserInputMultiContent = append(user.UserInputMultiContent, schema.MessageInputPart{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{MessagePartCommon: schema.MessagePartCommon{URL: &url}, Detail: schema.ImageURLDetailLow}})
		}
	}
	msg, err := m.Generate(ctx, []*schema.Message{schema.SystemMessage(system), user})
	if err != nil {
		return Result{}, err
	}
	if msg == nil {
		return Result{}, errors.New("empty model response")
	}
	result := Result{Content: msg.Content}
	if msg.ResponseMeta != nil && msg.ResponseMeta.Usage != nil {
		result.Usage = Usage{Input: msg.ResponseMeta.Usage.PromptTokens, Output: msg.ResponseMeta.Usage.CompletionTokens}
	}
	return result, nil
}
func newChat(ctx context.Context, cfg Provider) (cm.BaseChatModel, error) {
	if cfg.Model == "" || cfg.APIKey == "" {
		return nil, ErrUnavailable
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	switch cfg.Provider {
	case "ark", "":
		return arkmodel.NewChatModel(ctx, &arkmodel.ChatModelConfig{APIKey: cfg.APIKey, Model: cfg.Model, BaseURL: cfg.BaseURL, Timeout: &timeout, Thinking: &arkruntime.Thinking{Type: arkruntime.ThinkingTypeDisabled}})
	case "openai":
		return openaimodel.NewChatModel(ctx, &openaimodel.ChatModelConfig{APIKey: cfg.APIKey, Model: cfg.Model, BaseURL: cfg.BaseURL, HTTPClient: &http.Client{Timeout: timeout}})
	default:
		return nil, fmt.Errorf("unsupported model provider %q", cfg.Provider)
	}
}
func (c *Client) EmbeddingVersion() string {
	return strings.Join([]string{c.cfg.Embedding.Provider, c.cfg.Embedding.BaseURL, c.cfg.Embedding.Model, c.cfg.Embedding.APIType, fmt.Sprint(c.cfg.Embedding.Dimensions)}, "|")
}
func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	cfg := c.cfg.Embedding
	if cfg.APIKey == "" || cfg.Model == "" {
		return nil, ErrUnavailable
	}
	release, err := c.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	c.mu.Lock()
	em := c.embedding
	if em == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		switch cfg.Provider {
		case "ark", "":
			ec := &arkemb.EmbeddingConfig{APIKey: cfg.APIKey, Model: cfg.Model, BaseURL: cfg.BaseURL, Timeout: &timeout}
			if cfg.Dimensions > 0 {
				ec.Dimensions = &cfg.Dimensions
			}
			if cfg.APIType == "multimodal" {
				typ := arkemb.APITypeMultiModal
				ec.APIType = &typ
			}
			em, err = arkemb.NewEmbedder(ctx, ec)
		case "openai":
			ec := &openaiemb.EmbeddingConfig{APIKey: cfg.APIKey, Model: cfg.Model, BaseURL: cfg.BaseURL, HTTPClient: &http.Client{Timeout: timeout}}
			if cfg.Dimensions > 0 {
				ec.Dimensions = &cfg.Dimensions
			}
			em, err = openaiemb.NewEmbedder(ctx, ec)
		default:
			err = fmt.Errorf("unsupported embedding provider %q", cfg.Provider)
		}
		if err == nil {
			c.embedding = em
		}
	}
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	values, err := em.EmbedStrings(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(values) != 1 || len(values[0]) == 0 {
		return nil, errors.New("empty embedding")
	}
	if cfg.Dimensions > 0 && len(values[0]) != cfg.Dimensions {
		return nil, errors.New("embedding dimension mismatch")
	}
	out := make([]float32, len(values[0]))
	for i, v := range values[0] {
		out[i] = float32(v)
	}
	return out, nil
}
func JSON(raw string, v any) error {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```") {
		_, raw, _ = strings.Cut(raw, "\n")
		raw = strings.TrimSuffix(strings.TrimSpace(raw), "```")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing data in model JSON")
	}
	return nil
}
