package model

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/phlin/go-agent/internal/cache"
)

// Priority lets the gateway keep background indexing from consuming the
// capacity reserved for interactive chat.
type Priority uint8

const (
	PriorityBackground Priority = iota
	PriorityInteractive
)

type GenerateRequest struct {
	PromptVersion  string
	PersonaVersion string
	System         string
	Input          any
	Images         []string
	Priority       Priority
	GroupID        int64
	AttemptID      string
}

type EmbedRequest struct {
	ModelVersion string
	Text         string
	Priority     Priority
	GroupID      int64
	AttemptID    string
}

type Observation struct {
	Operation     string
	GroupID       int64
	AttemptID     string
	PromptVersion string
	ModelVersion  string
	CacheHit      bool
	Duration      time.Duration
	Usage         Usage
	Err           error
}

type ModelGateway interface {
	GenerateRequest(context.Context, GenerateRequest) (Result, error)
	EmbedRequest(context.Context, EmbedRequest) ([]float32, error)
}

// Gateway adapts the existing client interfaces to the request-shaped
// boundary. Keeping this adapter small allows the provider implementation to
// evolve without leaking configuration details into chat and memory packages.
type Gateway struct {
	Generator       Generator
	Embedder        Embedder
	Cache           cache.CacheHub
	EmbeddingTTL    time.Duration
	ChatSlots       chan struct{}
	VisionSlots     chan struct{}
	EmbeddingSlots  chan struct{}
	BackgroundSlots chan struct{}
	EmbeddingHits   atomic.Uint64
	EmbeddingMisses atomic.Uint64
	GenerateCalls   atomic.Uint64
	EmbeddingCalls  atomic.Uint64
	Observe         func(Observation)
}

func NewGateway(generator Generator, embedder Embedder, hub cache.CacheHub, embeddingTTL time.Duration, chat, vision, embedding, background int) *Gateway {
	if embeddingTTL <= 0 {
		embeddingTTL = 30 * time.Minute
	}
	return &Gateway{Generator: generator, Embedder: embedder, Cache: hub, EmbeddingTTL: embeddingTTL,
		ChatSlots: slots(chat), VisionSlots: slots(vision), EmbeddingSlots: slots(embedding), BackgroundSlots: slots(background)}
}

func slots(n int) chan struct{} {
	if n < 1 {
		n = 1
	}
	return make(chan struct{}, n)
}

func (g *Gateway) acquire(ctx context.Context, ch chan struct{}, priority Priority) (func(), error) {
	if ch == nil {
		return func() {}, nil
	}
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (g *Gateway) Generate(ctx context.Context, system string, input any, images []string) (Result, error) {
	return g.generate(ctx, GenerateRequest{System: system, Input: input, Images: images})
}

func (g *Gateway) generate(ctx context.Context, req GenerateRequest) (Result, error) {
	start := time.Now()
	if g == nil || g.Generator == nil {
		return Result{}, ErrUnavailable
	}
	priority := PriorityInteractive
	slots := g.ChatSlots
	if len(req.Images) > 0 {
		slots = g.VisionSlots
	}
	if _, ok := ctx.Value(backgroundKey{}).(bool); ok {
		priority = PriorityBackground
		slots = g.BackgroundSlots
	}
	release, err := g.acquire(ctx, slots, priority)
	if err != nil {
		if g != nil && g.Observe != nil {
			g.Observe(Observation{Operation: "generate", GroupID: req.GroupID, AttemptID: req.AttemptID, PromptVersion: req.PromptVersion, Duration: time.Since(start), Err: err})
		}
		return Result{}, err
	}
	defer release()
	result, err := g.Generator.Generate(ctx, req.System, req.Input, req.Images)
	if g != nil {
		g.GenerateCalls.Add(1)
		if g.Observe != nil {
			g.Observe(Observation{Operation: "generate", GroupID: req.GroupID, AttemptID: req.AttemptID, PromptVersion: req.PromptVersion, Duration: time.Since(start), Usage: result.Usage, Err: err})
		}
	}
	return result, err
}

func (g *Gateway) EmbeddingVersion() string {
	if g == nil || g.Embedder == nil {
		return ""
	}
	return g.Embedder.EmbeddingVersion()
}

func (g *Gateway) Embed(ctx context.Context, text string) ([]float32, error) {
	return g.embed(ctx, EmbedRequest{Text: text, ModelVersion: g.EmbeddingVersion()})
}

func (g *Gateway) embed(ctx context.Context, req EmbedRequest) ([]float32, error) {
	start := time.Now()
	if g == nil || g.Embedder == nil {
		return nil, ErrUnavailable
	}
	normalized := normalizeGatewayText(req.Text)
	if normalized == "" {
		return nil, ErrUnavailable
	}
	modelVersion := req.ModelVersion
	if modelVersion == "" {
		modelVersion = g.EmbeddingVersion()
	}
	key := cache.EmbeddingKey(modelVersion, normalized)
	load := func(call context.Context) (any, error) {
		slots := g.EmbeddingSlots
		if _, ok := call.Value(backgroundKey{}).(bool); ok {
			slots = g.BackgroundSlots
		}
		release, err := g.acquire(call, slots, PriorityInteractive)
		if err != nil {
			return nil, err
		}
		defer release()
		v, err := g.Embedder.Embed(call, normalized)
		if err != nil {
			return nil, err
		}
		return append([]float32(nil), v...), nil
	}
	if g.Cache == nil {
		value, err := load(ctx)
		if err != nil {
			return nil, err
		}
		vector := append([]float32(nil), value.([]float32)...)
		g.EmbeddingCalls.Add(1)
		if g.Observe != nil {
			g.Observe(Observation{Operation: "embedding", GroupID: req.GroupID, AttemptID: req.AttemptID, ModelVersion: modelVersion, Duration: time.Since(start), CacheHit: false, Err: err})
		}
		return vector, nil
	}
	if h, ok := g.Cache.(*cache.Hub); ok {
		var cached []float32
		if h.Get(key, &cached) {
			g.EmbeddingHits.Add(1)
			if g.Observe != nil {
				g.Observe(Observation{Operation: "embedding", GroupID: req.GroupID, AttemptID: req.AttemptID, ModelVersion: modelVersion, Duration: time.Since(start), CacheHit: true})
			}
			return cached, nil
		}
		g.EmbeddingMisses.Add(1)
		vector, typedErr := cache.DoTyped[[]float32](h, ctx, key, g.EmbeddingTTL, func(call context.Context) ([]float32, error) {
			loaded, loadErr := load(call)
			if loadErr != nil {
				return nil, loadErr
			}
			return loaded.([]float32), nil
		})
		g.EmbeddingCalls.Add(1)
		if g.Observe != nil {
			g.Observe(Observation{Operation: "embedding", GroupID: req.GroupID, AttemptID: req.AttemptID, ModelVersion: modelVersion, Duration: time.Since(start), CacheHit: false, Err: typedErr})
		}
		if typedErr != nil {
			return nil, typedErr
		}
		return append([]float32(nil), vector...), nil
	}
	var value any
	var err error
	if g.Cache.Get(key, &value) {
		g.EmbeddingHits.Add(1)
	} else {
		g.EmbeddingMisses.Add(1)
		value, err = g.Cache.Do(ctx, key, load)
	}
	if err != nil {
		g.EmbeddingCalls.Add(1)
		if g.Observe != nil {
			g.Observe(Observation{Operation: "embedding", GroupID: req.GroupID, AttemptID: req.AttemptID, ModelVersion: modelVersion, Duration: time.Since(start), CacheHit: false, Err: err})
		}
		return nil, err
	}
	vector, ok := value.([]float32)
	if !ok {
		return nil, ErrUnavailable
	}
	g.EmbeddingCalls.Add(1)
	if g.Observe != nil {
		g.Observe(Observation{Operation: "embedding", GroupID: req.GroupID, AttemptID: req.AttemptID, ModelVersion: modelVersion, Duration: time.Since(start), CacheHit: false})
	}
	return append([]float32(nil), vector...), nil
}

func (g *Gateway) GenerateRequest(ctx context.Context, req GenerateRequest) (Result, error) {
	return g.generate(ctx, req)
}

func (g *Gateway) EmbedRequest(ctx context.Context, req EmbedRequest) ([]float32, error) {
	return g.embed(ctx, req)
}

func normalizeGatewayText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}
