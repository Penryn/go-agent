package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/phlin/go-agent/internal/cache"
	"github.com/phlin/go-agent/internal/chat"
	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/conversation"
	"github.com/phlin/go-agent/internal/meme"
	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/model"
	"github.com/phlin/go-agent/internal/qq"
	"github.com/phlin/go-agent/internal/search/bm25"
	"github.com/phlin/go-agent/internal/store"
)

type App struct {
	cfg      config.Config
	store    *store.Store
	runtime  *chat.Runtime
	memories *memory.Service
	memes    *meme.Service
	topics   *conversation.TopicSummarizer
	qq       *qq.Client
	server   *http.Server
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	once     sync.Once
	closeErr error
}

func New(ctx context.Context, cfg config.Config) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	db, err := store.Open(ctx, cfg.Storage.DatabaseURL, cfg.Models.Embedding.Dimensions)
	if err != nil {
		return nil, err
	}
	child, cancel := context.WithCancel(ctx)
	a := &App{cfg: cfg, store: db, ctx: child, cancel: cancel}
	fail := func(err error) (*App, error) { cancel(); db.Close(); return nil, err }
	if err := os.MkdirAll(cfg.Meme.Path, 0700); err != nil {
		return fail(err)
	}
	chatSlots, visionSlots, embeddingSlots, backgroundSlots := cfg.ModelConcurrency.Budgets()
	models := model.New(cfg.Models, chatSlots, visionSlots, embeddingSlots, backgroundSlots)
	hub := cache.NewHub(cfg.Cache.MaxItems)
	gateway := model.NewGateway(models, models, hub, cfg.Cache.EmbeddingTTL, cfg.ModelConcurrency.Chat, cfg.ModelConcurrency.Vision, cfg.ModelConcurrency.Embedding, cfg.ModelConcurrency.Background)
	gateway.ModelVersion = func(images []string) string {
		if len(images) > 0 && cfg.Models.Vision.Model != "" {
			return cfg.Models.Vision.Model
		}
		return cfg.Models.Main.Model
	}
	gateway.Observe = func(o model.Observation) {
		attrs := []any{
			"operation", o.Operation,
			"group_id", o.GroupID,
			"attempt_id", o.AttemptID,
			"prompt_version", o.PromptVersion,
			"model_version", o.ModelVersion,
			"cache_hit", o.CacheHit,
			"duration_ms", o.Duration.Milliseconds(),
			"input_tokens", o.Usage.Input,
			"output_tokens", o.Usage.Output,
		}
		if o.ErrorClass != "" {
			attrs = append(attrs, "error_class", o.ErrorClass)
		}
		slog.Info("model request", attrs...)
	}
	a.memories = &memory.Service{Store: db, Model: gateway, Embedding: gateway, TopK: cfg.Memory.TopK, IndexLimit: cfg.Memory.IndexLimit, DistanceThreshold: cfg.Memory.SemanticDistanceThreshold, Budgets: memory.RecallBudgets{Preference: cfg.Memory.PreferenceBudget, Member: cfg.Memory.MemberBudget, Episode: cfg.Memory.EpisodeBudget}, Cache: hub, IndexTTL: cfg.Cache.IndexTTL, IndexCache: cache.New(cfg.Cache.MaxItems, func(index *bm25.Index) *bm25.Index { return index })}
	a.memes = &meme.Service{Store: db, Model: gateway, Embedding: gateway, Config: cfg.Meme, HTTP: qq.MediaClient(), Cache: hub}
	a.qq = &qq.Client{URL: cfg.QQ.OutboundURL, Token: cfg.QQ.OutboundToken, WSURL: cfg.QQ.EventWSURL, WSToken: cfg.QQ.EventWSToken}
	var sender chat.Sender
	if cfg.QQ.Enabled {
		if err := db.AcquireSender(ctx); err != nil {
			return fail(err)
		}
		sender = ownedSender{a.qq, db}
	}
	chatConfig := cfg.Chat
	chatConfig.Cache = chat.CacheTTL{Recall: cfg.Cache.RecallTTL, Context: cfg.Cache.ContextTTL, Sticker: cfg.Cache.StickerTTL, Image: cfg.Cache.ImageTTL}
	a.runtime = chat.New(child, &chat.Runtime{Store: db, Model: gateway, Embedding: gateway, Cache: hub, Memory: a.memories, Memes: a.memes, Sender: sender, Persona: cfg.Persona, Config: chatConfig, SelfID: cfg.QQ.SelfID, Groups: cfg.QQ.GroupWhitelist})
	a.memories.TopicResolver = a.runtime
	a.topics = &conversation.TopicSummarizer{Store: db, Model: gateway, Batch: cfg.Memory.SummaryBatch, Apply: a.runtime.ApplyTopicSummary}
	a.server = &http.Server{Addr: cfg.Server.HTTPListen, Handler: a.routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	return a, nil
}

type ownedSender struct {
	qq    *qq.Client
	store *store.Store
}

func (s ownedSender) Send(ctx context.Context, g int64, text, quote, path string) (qq.Receipt, error) {
	if err := s.store.CheckSender(ctx); err != nil {
		return qq.Receipt{}, &qq.SendError{Err: err, Definite: true}
	}
	if quote != "" {
		// Quote preconditions mirror a human using the client: refresh the
		// target, mark it read, and only then let the reply become visible.
		// Any failure ends the turn; degrading to an unquoted reply would
		// change the action the model chose.
		if err := s.qq.RefreshMessage(ctx, quote, g); err != nil {
			return qq.Receipt{}, err
		}
		if err := s.qq.MarkMessageRead(ctx, quote); err != nil {
			return qq.Receipt{}, err
		}
	}
	return s.qq.Send(ctx, g, text, quote, path)
}

// React runs the same quote preconditions before the emoji becomes visible,
// in the fixed order refresh → mark read → set_msg_emoji_like. A failure
// never falls back to a text substitute.
func (s ownedSender) React(ctx context.Context, g int64, messageID, emojiID string) error {
	if err := s.store.CheckSender(ctx); err != nil {
		return &qq.SendError{Err: err, Definite: true}
	}
	if err := s.qq.RefreshMessage(ctx, messageID, g); err != nil {
		return err
	}
	if err := s.qq.MarkMessageRead(ctx, messageID); err != nil {
		return err
	}
	return s.qq.React(ctx, messageID, emojiID)
}

// Poke checks send ownership, then delegates; the client refreshes membership
// before poking. Pokes never quote or mark messages read.
func (s ownedSender) Poke(ctx context.Context, g, user int64) error {
	if err := s.store.CheckSender(ctx); err != nil {
		return &qq.SendError{Err: err, Definite: true}
	}
	return s.qq.Poke(ctx, g, user)
}
func (a *App) Run(ctx context.Context) error {
	stop := context.AfterFunc(ctx, a.cancel)
	defer stop()
	defer a.Close()
	errorsCh := make(chan error, 2)
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errorsCh <- err
		}
	}()
	if a.cfg.QQ.Enabled {
		a.wg.Add(1)
		go func() {
			defer a.wg.Done()
			if err := a.qq.Receive(a.ctx, a.runtime.Submit); err != nil && !errors.Is(err, context.Canceled) {
				errorsCh <- err
			}
		}()
	}
	runBackground(a.ctx, &a.wg, a.cfg.Memory.Interval, a.memories.Tick)
	runBackground(a.ctx, &a.wg, a.cfg.Memory.SummaryInterval, a.topics.Tick)
	runBackground(a.ctx, &a.wg, 3*time.Second, a.memes.Tick)
	slog.Info("QQ group friend started", "listen", a.cfg.Server.HTTPListen, "qq_enabled", a.cfg.QQ.Enabled, "global_sticker_pool", true)
	select {
	case <-a.ctx.Done():
		return a.ctx.Err()
	case err := <-errorsCh:
		return err
	}
}
func (a *App) Close() error {
	a.once.Do(func() {
		a.cancel()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.server.Shutdown(shutdown); err != nil {
			_ = a.server.Close()
		}
		a.runtime.Close()
		a.wg.Wait()
		a.closeErr = a.store.Close()
	})
	return a.closeErr
}
