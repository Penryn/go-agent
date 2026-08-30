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
	humanruntime "github.com/phlin/go-agent/internal/humanbot/runtime"
	humandeliberation "github.com/phlin/go-agent/internal/humanbot/runtime/deliberation"
	humanactor "github.com/phlin/go-agent/internal/humanbot/runtime/group_actor"
	humaningress "github.com/phlin/go-agent/internal/humanbot/runtime/ingress"
	humanperception "github.com/phlin/go-agent/internal/humanbot/runtime/perception"
	humanreflection "github.com/phlin/go-agent/internal/humanbot/runtime/reflection"
	backgroundruntime "github.com/phlin/go-agent/internal/runtime/background"
	"github.com/phlin/go-agent/internal/runtime/scheduler"
	actionsvc "github.com/phlin/go-agent/internal/services/action"
	contextsvc "github.com/phlin/go-agent/internal/services/context"
	learningsvc "github.com/phlin/go-agent/internal/services/learning"
	memesvc "github.com/phlin/go-agent/internal/services/meme"
	memsvc "github.com/phlin/go-agent/internal/services/memory"
	multimodalsvc "github.com/phlin/go-agent/internal/services/multimodal"
	normalizersvc "github.com/phlin/go-agent/internal/services/normalizer"
	outputguardsvc "github.com/phlin/go-agent/internal/services/outputguard"
	personasvc "github.com/phlin/go-agent/internal/services/persona"
	policysvc "github.com/phlin/go-agent/internal/services/policy"
	promptingsvc "github.com/phlin/go-agent/internal/services/prompting"
	reviewsvc "github.com/phlin/go-agent/internal/services/review"
	toolsvc "github.com/phlin/go-agent/internal/services/tools"
)

type App struct {
	cfg          config.Config
	humanRuntime *humanruntime.Runtime
	background   *backgroundruntime.Runtime
	inbound      ports.InboundSource
	server       *http.Server
	sched        *scheduler.Scheduler
	closeOnce    sync.Once
	closeErr     error
	cleanup      func() error
	healthCheck  func(context.Context) error
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
	eventLog := humaningress.NewMemoryEventLog()
	presenceManager := humanactor.NewManager(eventLog, humanactor.WithArchive(stores.memory))
	contextService.WithWorkingMemory(presenceManager)
	if cfg.Memory.SemanticTopK > 0 || cfg.Memory.SemanticThreshold > 0 {
		contextService.WithSemanticConfig(cfg.Memory.SemanticTopK, cfg.Memory.SemanticThreshold)
	}

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
	visionService := multimodalsvc.New(modelFactory, cfg.Multimodal)
	perceptionPipeline := humanperception.New(visionService, memeService, presenceManager, backgroundRuntime)

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
	executor := actionsvc.New(sender, memeService, guard,
		actionsvc.WithBackgroundRuntime(backgroundRuntime),
		actionsvc.WithPresenceObserver(presenceManager),
		actionsvc.WithSelfID(cfg.QQ.SelfID),
	)

	// F2 PersonaService：情绪状态动态驱动
	moodSvc := personasvc.New(stores.state, cfg.Persona.ID)
	turnObserver := humanreflection.New(stores.state, moodSvc, time.Duration(cfg.Autonomy.MinReplyIntervalSec)*time.Second)

	// Human Presence Runtime owns ingress, per-group working memory, candidate
	// scheduling, deliberation, realization, and outbound self-observation.
	// Context projection and planner compatibility remain behind this adapter.
	deliberator := humandeliberation.NewAdapter(contextService, planner)
	humanRuntime := humanruntime.New(ctx, normalizer, presenceManager, deliberator, perceptionPipeline, turnObserver, executor, humanruntime.Config{
		GroupWhitelist: cfg.QQ.GroupWhitelist,
		SelfID:         cfg.QQ.SelfID,
		JobTimeout:     120 * time.Second,
	})

	// Scheduler：注册所有定时任务
	sched := scheduler.New()
	moodSvc.RegisterJobs(sched, cfg.QQ.GroupWhitelist)

	// learning service：接入运行时，每 6 小时对白名单群跑一次增量学习
	reviewService := reviewsvc.New(memorySvc)
	learningSvc, learnErr := learningsvc.New(ctx, stores.memory, stores.learning, reviewService)
	if learnErr != nil {
		_ = backgroundRuntime.Close(context.Background())
		_ = stores.Close()
		return nil, fmt.Errorf("learning service init: %w", learnErr)
	}
	learningSvc.RegisterJobs(sched, cfg.QQ.GroupWhitelist)

	app := &App{
		cfg:          cfg,
		humanRuntime: humanRuntime,
		background:   backgroundRuntime,
		sched:        sched,
		cleanup: func() error {
			return errors.Join(humanRuntime.Close(), presenceManager.Close(), stores.Close())
		},
		healthCheck: stores.HealthCheck,
	}
	if cfg.QQ.Enabled && cfg.QQ.EventWSURL != "" {
		app.inbound = inboundnapcat.NewWSReceiver(cfg.QQ.EventWSURL, cfg.QQ.AccessToken)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", app.handleHealth)
	mux.Handle(cfg.QQ.InboundRoute, inboundnapcat.NewHandler(cfg.QQ.AccessToken, func(ctx context.Context, payload []byte) (any, error) {
		if err := app.humanRuntime.SubmitRaw(ctx, payload); err != nil {
			slog.Warn("human runtime: http event rejected", "err", err)
		}
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

	errCh := make(chan error, 1)
	go func() {
		if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	if a.inbound != nil {
		go func() {
			if err := a.inbound.Receive(ctx, func(inner context.Context, payload []byte) error {
				return a.humanRuntime.SubmitRaw(inner, payload)
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

func (a *App) ProcessRawEvent(ctx context.Context, payload []byte) (humanruntime.Outcome, error) {
	return a.humanRuntime.ProcessRawEvent(ctx, payload)
}

func (a *App) Close() error {
	a.closeOnce.Do(func() {
		if a.sched != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			a.closeErr = a.sched.Close(ctx)
			cancel()
		}
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
