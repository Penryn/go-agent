package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	inboundnapcat "github.com/phlin/go-agent/internal/adapters/inbound/napcat"
	"github.com/phlin/go-agent/internal/adapters/inmemory"
	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
	outboundnapcat "github.com/phlin/go-agent/internal/adapters/outbound/napcat"
	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/core/ports"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
	humandomain "github.com/phlin/go-agent/internal/humanbot/domain"
	humanruntime "github.com/phlin/go-agent/internal/humanbot/runtime"
	humandeliberation "github.com/phlin/go-agent/internal/humanbot/runtime/deliberation"
	humanactor "github.com/phlin/go-agent/internal/humanbot/runtime/group_actor"
	humaningress "github.com/phlin/go-agent/internal/humanbot/runtime/ingress"
	humanperception "github.com/phlin/go-agent/internal/humanbot/runtime/perception"
	humanreflection "github.com/phlin/go-agent/internal/humanbot/runtime/reflection"
	backgroundruntime "github.com/phlin/go-agent/internal/runtime/background"
	outboxruntime "github.com/phlin/go-agent/internal/runtime/outbox"
	"github.com/phlin/go-agent/internal/runtime/scheduler"
	actionsvc "github.com/phlin/go-agent/internal/services/action"
	contextsvc "github.com/phlin/go-agent/internal/services/context"
	curatorsvc "github.com/phlin/go-agent/internal/services/curator"
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
	cfg          config.Config
	humanRuntime *humanruntime.Runtime
	background   *backgroundruntime.Runtime
	outbox       *outboxruntime.Runtime
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

	vectorGraph := buildVectorGraph(ctx, cfg, modelFactory, stores)
	qdrantVectorStore := vectorGraph.memory
	memeVectorStore := vectorGraph.meme

	// contextService 需要 VectorMemoryStore（无 Qdrant 时降级为 NoopVectorStore）
	var vectorStore ports.VectorMemoryStore = ports.NoopVectorStore{}
	if qdrantVectorStore != nil {
		vectorStore = qdrantVectorStore
	}
	contextService := contextsvc.New(stores.memory, vectorStore, stores.profile, stores.state, policyService, cfg.Persona)
	eventLog := humaningress.NewMemoryEventLog()
	actorOptions := []humanactor.Option{
		humanactor.WithArchive(stores.memory),
		humanactor.WithIdleTTL(mustDuration(cfg.Runtime.ActorIdleTTL, 30*time.Minute)),
	}
	if stateStore, ok := stores.memory.(humanactor.WorkingMemoryStore); ok {
		actorOptions = append(actorOptions, humanactor.WithStateStore(stateStore))
	}
	presenceManager := humanactor.NewManager(eventLog, actorOptions...)
	contextService.WithWorkingMemory(presenceManager)
	if cfg.Memory.SemanticTopK > 0 || cfg.Memory.SemanticThreshold > 0 {
		contextService.WithSemanticConfig(cfg.Memory.SemanticTopK, cfg.Memory.SemanticThreshold)
	}

	backgroundRuntime := backgroundruntime.New(context.WithoutCancel(ctx), backgroundruntime.Config{
		QueueSize:   cfg.Runtime.QueueLength,
		WorkerCount: cfg.Runtime.WorkerCount,
	})
	var durableOutbox *outboxruntime.Runtime
	if outboxStore, ok := stores.memory.(ports.OutboxStore); ok {
		durableOutbox = outboxruntime.New(context.WithoutCancel(ctx), outboxStore, outboxruntime.Config{
			WorkerCount: cfg.Runtime.WorkerCount,
		})
	}

	// memorySvc：有 Qdrant 时注入 WithVectorStore；同时注入差异化 TTL 配置
	memOpts := []memsvc.Option{}
	if qdrantVectorStore != nil {
		memOpts = append(memOpts, memsvc.WithVectorStore(qdrantVectorStore))
	}
	if len(cfg.Memory.TypeTTL) > 0 || cfg.Memory.DefaultTTL != "" {
		memOpts = append(memOpts, memsvc.WithTypeTTL(cfg.Memory.TypeTTL, cfg.Memory.DefaultTTL))
	}
	memOpts = append(memOpts, memsvc.WithBackgroundRuntime(backgroundRuntime))
	if durableOutbox != nil {
		memOpts = append(memOpts, memsvc.WithOutbox(durableOutbox))
	}
	memorySvc := memsvc.New(stores.memory, memOpts...)
	if durableOutbox != nil {
		if err := durableOutbox.Register("memory_vector_index", func(jobCtx context.Context, payload []byte) error {
			var record memorydomain.MemoryRecord
			if err := json.Unmarshal(payload, &record); err != nil {
				return fmt.Errorf("decode memory vector task: %w", err)
			}
			return memorySvc.ProcessVectorIndex(jobCtx, record)
		}); err != nil {
			_ = durableOutbox.Close()
			_ = backgroundRuntime.Close(context.Background())
			_ = stores.Close()
			return nil, fmt.Errorf("register memory outbox handler: %w", err)
		}
	}
	memeService := memesvc.New(stores.meme, cfg.Meme,
		memesvc.WithVectorStore(memeVectorStore),
		memesvc.WithBackgroundRuntime(backgroundRuntime),
	)
	if durableOutbox != nil {
		if err := durableOutbox.Register("meme_vector_index", func(jobCtx context.Context, payload []byte) error {
			var task memesvc.VectorIndexTask
			if err := json.Unmarshal(payload, &task); err != nil {
				return fmt.Errorf("decode meme vector task: %w", err)
			}
			return memeService.ProcessVectorIndex(jobCtx, task)
		}); err != nil {
			_ = durableOutbox.Close()
			_ = backgroundRuntime.Close(context.Background())
			_ = stores.Close()
			return nil, fmt.Errorf("register meme outbox handler: %w", err)
		}
	}
	visionService := multimodalsvc.New(modelFactory, cfg.Multimodal)
	perceptionOpts := []humanperception.Option{}
	if durableOutbox != nil {
		perceptionOpts = append(perceptionOpts, humanperception.WithOutbox(durableOutbox))
	}
	perceptionPipeline := humanperception.New(visionService, memeService, presenceManager, backgroundRuntime, perceptionOpts...)
	if durableOutbox != nil {
		if err := durableOutbox.Register("perception_event", func(jobCtx context.Context, payload []byte) error {
			var record humandomain.EventRecord
			if err := json.Unmarshal(payload, &record); err != nil {
				return fmt.Errorf("decode perception event: %w", err)
			}
			return perceptionPipeline.Process(jobCtx, record)
		}); err != nil {
			_ = durableOutbox.Close()
			_ = backgroundRuntime.Close(context.Background())
			_ = stores.Close()
			return nil, fmt.Errorf("register perception outbox handler: %w", err)
		}
	}

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
	guard := outputguardsvc.New(cfg.Persona.ReplyMaxChars*2, cfg.Persona.ReplyMaxSentences+1)
	actionOpts := []actionsvc.Option{
		actionsvc.WithBackgroundRuntime(backgroundRuntime),
		actionsvc.WithPresenceObserver(presenceManager),
		actionsvc.WithSelfID(cfg.QQ.SelfID),
	}
	if durableOutbox != nil {
		actionOpts = append(actionOpts, actionsvc.WithOutbox(durableOutbox))
	}
	executor := actionsvc.New(sender, memeService, guard, actionOpts...)
	if durableOutbox != nil {
		if err := durableOutbox.Register("meme_mark_sent", func(jobCtx context.Context, payload []byte) error {
			var task actionsvc.MarkMemeSentTask
			if err := json.Unmarshal(payload, &task); err != nil {
				return fmt.Errorf("decode meme mark-sent task: %w", err)
			}
			return memeService.MarkSent(jobCtx, task.MemeID)
		}); err != nil {
			_ = durableOutbox.Close()
			_ = backgroundRuntime.Close(context.Background())
			_ = stores.Close()
			return nil, fmt.Errorf("register meme sent outbox handler: %w", err)
		}
	}

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
		WorkerCount:    cfg.Runtime.WorkerCount,
	})
	if thoughtStore, ok := stores.memory.(ports.ThoughtStore); ok {
		humanRuntime.SetThoughtStore(thoughtStore)
	}

	// Scheduler：注册所有定时任务
	sched := scheduler.New()
	moodSvc.RegisterJobs(sched, cfg.QQ.GroupWhitelist)

	// learning service：接入运行时，每 6 小时对白名单群跑一次增量学习
	reviewService := reviewsvc.New(memorySvc)
	profileService := profilesvc.New(stores.profile, cfg.Persona.ID)
	curatorService, curatorErr := curatorsvc.New(ctx)
	if curatorErr != nil {
		_ = backgroundRuntime.Close(context.Background())
		_ = stores.Close()
		return nil, fmt.Errorf("curator service init: %w", curatorErr)
	}
	if durableOutbox != nil {
		if err := durableOutbox.Register("curator_turn", func(jobCtx context.Context, payload []byte) error {
			var snapshot conversationdomain.ContextSnapshot
			if err := json.Unmarshal(payload, &snapshot); err != nil {
				return fmt.Errorf("decode curator snapshot: %w", err)
			}
			out, err := curatorService.Run(jobCtx, curatorsvc.Input{Snapshot: snapshot})
			if err != nil {
				return err
			}
			return reviewService.ApplyCurator(jobCtx, out.MemoryIntents)
		}); err != nil {
			_ = durableOutbox.Close()
			_ = backgroundRuntime.Close(context.Background())
			_ = stores.Close()
			return nil, fmt.Errorf("register curator outbox handler: %w", err)
		}
	}
	humanRuntime.AddEventObserver(profileService.ObserveEvent)
	humanRuntime.AddCompletedTurnObserver(humanruntime.CompletedTurnObserverFunc(func(turnCtx context.Context, snapshot conversationdomain.ContextSnapshot, receipt replydomain.ActionReceipt) error {
		if durableOutbox != nil {
			payload, err := json.Marshal(snapshot)
			if err != nil {
				return fmt.Errorf("encode curator snapshot: %w", err)
			}
			key := snapshot.SnapshotID
			if key == "" {
				key = snapshot.Event.EventID
			}
			return durableOutbox.Enqueue(turnCtx, "curator_turn", key, payload)
		}
		ok := backgroundRuntime.Submit(backgroundruntime.Job{
			Name:    "curator_turn",
			Timeout: 30 * time.Second,
			Run: func(jobCtx context.Context) error {
				out, err := curatorService.Run(jobCtx, curatorsvc.Input{Snapshot: snapshot})
				if err != nil {
					return err
				}
				if err := reviewService.ApplyCurator(jobCtx, out.MemoryIntents); err != nil {
					return err
				}
				_ = receipt
				return nil
			},
		})
		if !ok {
			return fmt.Errorf("curator job queue is full")
		}
		return nil
	}))
	learningOpts := []learningsvc.Option{}
	if durableOutbox != nil {
		learningOpts = append(learningOpts, learningsvc.WithOutbox(durableOutbox))
	}
	learningSvc, learnErr := learningsvc.New(ctx, stores.memory, stores.learning, reviewService, learningOpts...)
	if learnErr != nil {
		_ = backgroundRuntime.Close(context.Background())
		_ = stores.Close()
		return nil, fmt.Errorf("learning service init: %w", learnErr)
	}
	if durableOutbox != nil {
		if err := durableOutbox.Register("learning_extract", func(jobCtx context.Context, payload []byte) error {
			var task struct {
				GroupID int64 `json:"group_id"`
			}
			if err := json.Unmarshal(payload, &task); err != nil {
				return fmt.Errorf("decode learning task: %w", err)
			}
			return learningSvc.ProcessGroup(jobCtx, task.GroupID)
		}); err != nil {
			_ = durableOutbox.Close()
			_ = backgroundRuntime.Close(context.Background())
			_ = stores.Close()
			return nil, fmt.Errorf("register learning outbox handler: %w", err)
		}
	}
	learningSvc.RegisterJobs(sched, cfg.QQ.GroupWhitelist)

	app := &App{
		cfg:          cfg,
		humanRuntime: humanRuntime,
		background:   backgroundRuntime,
		outbox:       durableOutbox,
		sched:        sched,
		cleanup: func() error {
			var outboxErr error
			if durableOutbox != nil {
				outboxErr = durableOutbox.Close()
			}
			return errors.Join(outboxErr, humanRuntime.Close(), presenceManager.Close(), stores.Close())
		},
		healthCheck: stores.HealthCheck,
	}
	if cfg.QQ.Enabled && cfg.QQ.EventWSURL != "" {
		app.inbound = inboundnapcat.NewWSReceiver(cfg.QQ.EventWSURL, cfg.QQ.AccessToken)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", app.handleHealth)

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
			a.closeErr = errors.Join(a.closeErr, a.sched.Close(ctx))
			cancel()
		}
		if a.background != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			a.closeErr = errors.Join(a.closeErr, a.background.Close(ctx))
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
