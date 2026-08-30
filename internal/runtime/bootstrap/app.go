package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	inboundnapcat "github.com/phlin/go-agent/internal/adapters/inbound/napcat"
	"github.com/phlin/go-agent/internal/adapters/inmemory"
	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
	outboundnapcat "github.com/phlin/go-agent/internal/adapters/outbound/napcat"
	qdrantstore "github.com/phlin/go-agent/internal/adapters/storage/qdrant"
	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/core/ports"
	"github.com/phlin/go-agent/internal/core/usecase"
	backgroundruntime "github.com/phlin/go-agent/internal/runtime/background"
	"github.com/phlin/go-agent/internal/runtime/dispatcher"
	"github.com/phlin/go-agent/internal/runtime/scheduler"
	turnruntime "github.com/phlin/go-agent/internal/runtime/turn"
	actionsvc "github.com/phlin/go-agent/internal/services/action"
	autonomysvc "github.com/phlin/go-agent/internal/services/autonomy"
	contextsvc "github.com/phlin/go-agent/internal/services/context"
	gatesvc "github.com/phlin/go-agent/internal/services/gate"
	learningsvc "github.com/phlin/go-agent/internal/services/learning"
	memesvc "github.com/phlin/go-agent/internal/services/meme"
	memsvc "github.com/phlin/go-agent/internal/services/memory"
	multimodalsvc "github.com/phlin/go-agent/internal/services/multimodal"
	normalizersvc "github.com/phlin/go-agent/internal/services/normalizer"
	outputguardsvc "github.com/phlin/go-agent/internal/services/outputguard"
	personasvc "github.com/phlin/go-agent/internal/services/persona"
	policysvc "github.com/phlin/go-agent/internal/services/policy"
	profilesvc "github.com/phlin/go-agent/internal/services/profile"
	promptingsvc "github.com/phlin/go-agent/internal/services/prompting"
	reviewsvc "github.com/phlin/go-agent/internal/services/review"
	toolsvc "github.com/phlin/go-agent/internal/services/tools"
)

type App struct {
	cfg         config.Config
	turnRuntime *turnruntime.Runtime
	background  *backgroundruntime.Runtime
	normalizer  *normalizersvc.Service
	dispatcher  *dispatcher.GroupDispatcher
	inbound     ports.InboundSource
	server      *http.Server
	sched       *scheduler.Scheduler
	closeOnce   sync.Once
	closeErr    error
	cleanup     func() error
	healthCheck func(context.Context) error
}

func NewApp(ctx context.Context, cfg config.Config) (*App, error) {
	stores, err := newStoreBundle(ctx, cfg)
	if err != nil {
		return nil, err
	}

	var sender ports.OutboundSender = inmemory.NewSender()
	if cfg.QQ.Enabled && cfg.QQ.OutboundURL != "" {
		sender = outboundnapcat.NewSender(cfg.QQ.OutboundURL, cfg.QQ.AccessToken, nil)
	}

	policyService := policysvc.New(cfg)
	modelFactory := modeladapter.NewFactory(cfg.Models)
	if err := modelFactory.Warmup(ctx); err != nil {
		_ = stores.Close()
		return nil, err
	}

	// Qdrant 可选初始化（依赖 embedder + vector_dim 配置）
	var qdrantVectorStore *qdrantstore.Store
	var memeVectorStore ports.VectorMemeStore = ports.NoopVectorMemeStore{}
	if cfg.Storage.Qdrant.VectorDim > 0 {
		embedder, embErr := modelFactory.EmbeddingModel(ctx)
		if embErr != nil {
			slog.Warn("app: embedding model unavailable, skipping qdrant init", "err", embErr)
		} else {
			topK := cfg.Memory.SemanticTopK
			if topK <= 0 {
				topK = 6
			}
			qs, qdrantErr := qdrantstore.New(ctx, cfg.Storage.Qdrant, embedder, cfg.Storage.Qdrant.VectorDim, topK)
			if qdrantErr != nil {
				slog.Warn("app: qdrant init failed, vector search disabled", "err", qdrantErr)
			} else {
				qdrantVectorStore = qs
				stores.closeFn = append(stores.closeFn, qs.Close)
				stores.probeFn = append(stores.probeFn, qs.Ping)

				// 表情包向量存储（可选，依赖 meme_collection 配置）
				if cfg.Storage.Qdrant.MemeCollection != "" {
					memeTopK := cfg.Meme.SemanticTopK
					if memeTopK <= 0 {
						memeTopK = 5
					}
					mvs, mvsErr := qdrantstore.NewMemeVectorStore(ctx, qs.Client(), cfg.Storage.Qdrant.MemeCollection, embedder, cfg.Storage.Qdrant.VectorDim, memeTopK)
					if mvsErr != nil {
						slog.Warn("app: meme vector store init failed, meme semantic search disabled", "err", mvsErr)
					} else {
						memeVectorStore = mvs
					}
				}
			}
		}
	}

	// contextService 需要 VectorMemoryStore（无 Qdrant 时降级为 NoopVectorStore）
	var vectorStore ports.VectorMemoryStore = ports.NoopVectorStore{}
	if qdrantVectorStore != nil {
		vectorStore = qdrantVectorStore
	}
	contextService := contextsvc.New(stores.memory, vectorStore, stores.profile, stores.state, policyService, cfg.Persona)
	if cfg.Memory.SemanticTopK > 0 || cfg.Memory.SemanticThreshold > 0 {
		contextService.WithSemanticConfig(cfg.Memory.SemanticTopK, cfg.Memory.SemanticThreshold)
	}

	gateService := gatesvc.New(modelFactory)
	autonomyService := autonomysvc.New(policyService, gateService)
	visionService := multimodalsvc.New(modelFactory, cfg.Multimodal)
	backgroundRuntime := backgroundruntime.New(context.WithoutCancel(ctx), backgroundruntime.Config{
		QueueSize:   cfg.Runtime.QueueLength,
		WorkerCount: cfg.Runtime.WorkerCount,
	})

	// memorySvc：有 Qdrant 时注入 WithVectorStore；同时注入差异化 TTL 配置
	memOpts := []memsvc.Option{}
	if qdrantVectorStore != nil {
		memOpts = append(memOpts, memsvc.WithVectorStore(qdrantVectorStore))
	}
	if len(cfg.Memory.TypeTTL) > 0 || cfg.Memory.DefaultTTL != "" {
		memOpts = append(memOpts, memsvc.WithTypeTTL(cfg.Memory.TypeTTL, cfg.Memory.DefaultTTL))
	}
	memOpts = append(memOpts, memsvc.WithBackgroundRuntime(backgroundRuntime))
	memorySvc := memsvc.New(stores.memory, memOpts...)
	memeService := memesvc.New(stores.meme, cfg.Meme,
		memesvc.WithVectorStore(memeVectorStore),
		memesvc.WithBackgroundRuntime(backgroundRuntime),
	)

	toolRuntime := toolsvc.NewRuntime(stores.memory, stores.meme,
		toolsvc.WithProfileStore(stores.profile),
		toolsvc.WithPersonaID(cfg.Persona.ID),
		toolsvc.WithMemoryService(memorySvc),
		toolsvc.WithMemeService(memeService),
	)
	fallbackPlanner := promptingsvc.NewDeterministicPlanner(cfg.Persona)
	planner := promptingsvc.NewAgentPlanner(
		modelFactory,
		toolRuntime,
		promptingsvc.NewComposer(cfg.Persona),
		fallbackPlanner,
	)
	normalizer := normalizersvc.New("onebot", cfg.QQ.SelfID, cfg.Persona.Aliases)

	// F1 OutputGuard：从 persona 配置读取截断阈值
	guard := outputguardsvc.New(
		outputguardsvc.WithMaxChars(cfg.Persona.ReplyMaxChars),
		outputguardsvc.WithMaxSentences(cfg.Persona.ReplyMaxSentences),
	)
	executor := actionsvc.New(sender, memeService, guard, actionsvc.WithBackgroundRuntime(backgroundRuntime))

	// F2 PersonaService：情绪状态动态驱动
	moodSvc := personasvc.New(stores.state, cfg.Persona.ID)

	profileService := profilesvc.New(stores.profile, cfg.Persona.ID)

	processor := usecase.NewProcessor(
		normalizer,
		contextService,
		autonomyService,
		planner,
		executor,
		stores.memory,
		stores.state,
		cfg.QQ.GroupWhitelist,
		cfg.Persona.ID,
		usecase.WithMemoryService(memorySvc),
		usecase.WithProfileService(profileService),
		usecase.WithMemeService(memeService),
		usecase.WithVisionService(visionService),
		usecase.WithPersonaService(moodSvc),
		usecase.WithBackgroundRuntime(backgroundRuntime),
	)
	turnRuntime := turnruntime.New(processor)

	// Scheduler：注册所有定时任务
	sched := scheduler.New()
	moodSvc.RegisterJobs(sched, cfg.QQ.GroupWhitelist)

	// learning service：接入运行时，每 6 小时对白名单群跑一次增量学习
	reviewService := reviewsvc.New(memorySvc)
	learningSvc, learnErr := learningsvc.New(ctx, stores.memory, reviewService)
	if learnErr != nil {
		_ = backgroundRuntime.Close(context.Background())
		_ = stores.Close()
		return nil, fmt.Errorf("learning service init: %w", learnErr)
	}
	learningSvc.RegisterJobs(sched, cfg.QQ.GroupWhitelist)

	app := &App{
		cfg:         cfg,
		turnRuntime: turnRuntime,
		background:  backgroundRuntime,
		normalizer:  normalizer,
		sched:       sched,
		cleanup:     stores.Close,
		healthCheck: stores.HealthCheck,
	}
	if cfg.QQ.Enabled && cfg.QQ.EventWSURL != "" {
		app.inbound = inboundnapcat.NewWSReceiver(cfg.QQ.EventWSURL, cfg.QQ.AccessToken)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", app.handleHealth)
	// HTTP handler 通过 app.dispatch 调用 dispatcher；
	// dispatcher 在 Run() 时才被初始化，此处闭包延迟求值，运行期安全。
	mux.Handle(cfg.QQ.InboundRoute, inboundnapcat.NewHandler(cfg.QQ.AccessToken, func(ctx context.Context, payload []byte) (any, error) {
		app.dispatch(ctx, payload)
		return struct{}{}, nil
	}))

	app.server = &http.Server{
		Addr:         cfg.Server.HTTPListen,
		Handler:      mux,
		ReadTimeout:  mustDuration(cfg.Server.ReadTimeout, 5*time.Second),
		WriteTimeout: mustDuration(cfg.Server.WriteTimeout, 10*time.Second),
	}

	return app, nil
}

func (a *App) Run(ctx context.Context) error {
	// 启动定时任务调度器（情绪衰减等 background job）
	a.sched.Start(ctx)

	// 用运行期 ctx 初始化 dispatcher，使 worker goroutine 的生命周期绑定到 ctx。
	a.dispatcher = dispatcher.New(
		ctx,
		a.normalizer,
		a.turnRuntime,
		dispatcher.DefaultConfig(),
	)

	errCh := make(chan error, 1)
	go func() {
		if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	if a.inbound != nil {
		go func() {
			// WS 入口：readLoop 是串行的，handler 同步返回后才读下一条。
			// 改为调用 dispatcher.Dispatch，将消息投入对应群的串行队列后立即返回，
			// 不等待处理完成，从而让 readLoop 可以尽快读取下一条消息。
			if err := a.inbound.Receive(ctx, func(inner context.Context, payload []byte) error {
				a.dispatch(inner, payload)
				return nil
			}); err != nil && !errors.Is(err, context.Canceled) {
				errCh <- err
			}
		}()
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.server.Shutdown(shutdownCtx)
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

// dispatch 将 payload 投递给 dispatcher。
// 在 Run() 启动前（dispatcher 为 nil）的极端情况下，降级为直接同步处理。
func (a *App) dispatch(ctx context.Context, payload []byte) {
	if a.dispatcher != nil {
		a.dispatcher.Dispatch(ctx, payload)
		return
	}
	// 降级路径：dispatcher 尚未初始化时直接调用（理论上不会发生）
	if _, err := a.turnRuntime.ProcessRawEvent(ctx, payload); err != nil {
		slog.Error("dispatch fallback: process raw event failed", "error", err)
	}
}

func (a *App) ProcessRawEvent(ctx context.Context, payload []byte) (turnruntime.Outcome, error) {
	return a.turnRuntime.ProcessRawEvent(ctx, payload)
}

func (a *App) Close() error {
	a.closeOnce.Do(func() {
		if a.background != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			a.closeErr = a.background.Close(ctx)
			cancel()
		}
		if a.cleanup != nil {
			a.closeErr = errors.Join(a.closeErr, a.cleanup())
		}
	})
	return a.closeErr
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if a.healthCheck != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := a.healthCheck(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, `{"ok":false,"error":%q}`, err.Error())
			return
		}
	}

	_, _ = w.Write([]byte(`{"ok":true}`))
}

func mustDuration(raw string, fallback time.Duration) time.Duration {
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return value
}
