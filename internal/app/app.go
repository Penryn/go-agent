package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	inboundnapcat "github.com/phlin/go-agent/internal/adapters/inbound/napcat"
	"github.com/phlin/go-agent/internal/adapters/inmemory"
	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
	outboundnapcat "github.com/phlin/go-agent/internal/adapters/outbound/napcat"
	actionsvc "github.com/phlin/go-agent/internal/application/action"
	contextsvc "github.com/phlin/go-agent/internal/application/context"
	learningsvc "github.com/phlin/go-agent/internal/application/learning"
	memesvc "github.com/phlin/go-agent/internal/application/meme"
	memsvc "github.com/phlin/go-agent/internal/application/memory"
	multimodalsvc "github.com/phlin/go-agent/internal/application/multimodal"
	normalizersvc "github.com/phlin/go-agent/internal/application/normalizer"
	outputguardsvc "github.com/phlin/go-agent/internal/application/outputguard"
	personasvc "github.com/phlin/go-agent/internal/application/persona"
	policysvc "github.com/phlin/go-agent/internal/application/policy"
	"github.com/phlin/go-agent/internal/application/textutil"
	"github.com/phlin/go-agent/internal/application/ports"
	presenceruntime "github.com/phlin/go-agent/internal/application/presence"
	presencedeliberation "github.com/phlin/go-agent/internal/application/presence/deliberation"
	presenceactor "github.com/phlin/go-agent/internal/application/presence/group_actor"
	presenceingress "github.com/phlin/go-agent/internal/application/presence/ingress"
	presenceperception "github.com/phlin/go-agent/internal/application/presence/perception"
	presencereflection "github.com/phlin/go-agent/internal/application/presence/reflection"
	profilesvc "github.com/phlin/go-agent/internal/application/profile"
	promptingsvc "github.com/phlin/go-agent/internal/application/prompting"
	retrievalsvc "github.com/phlin/go-agent/internal/application/retrieval"
	outboxruntime "github.com/phlin/go-agent/internal/application/runtime/outbox"
	"github.com/phlin/go-agent/internal/application/runtime/scheduler"
	toolsvc "github.com/phlin/go-agent/internal/application/tools"
	"github.com/phlin/go-agent/internal/config"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
	replydomain "github.com/phlin/go-agent/internal/domain/reply"
)

type App struct {
	cfg          config.Config
	humanRuntime *presenceruntime.Runtime
	inbound      *inboundnapcat.WSReceiver
	server       *http.Server
	sched        *scheduler.Scheduler
	closeOnce    sync.Once
	closeErr     error
	cleanup      func() error
	healthCheck  func(context.Context) error
}

func New(ctx context.Context, cfg config.Config) (*App, error) {
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
	vectorMemoryStore := vectorGraph.memory
	memeVectorStore := vectorGraph.meme
	hybridRetrieval := retrievalsvc.New(stores.memory, stores.meme, vectorMemoryStore, memeVectorStore, retrievalsvc.Config{
		MemoryCandidateK: max(cfg.Memory.TopK*5, 20),
		MemeCandidateK:   max(cfg.Meme.SearchTopK*5, 20),
		MemoryThreshold:  cfg.Memory.SemanticThreshold,
		MemeThreshold:    cfg.Meme.SemanticThreshold,
	})

	contextService := contextsvc.New(stores.memory, stores.profile, stores.state, policyService, cfg.Persona, hybridRetrieval, cfg.Memory.TopK)
	contextService.WithPersonaFactStore(stores.personaFacts)
	eventLog := presenceingress.NewMemoryEventLog()
	actorOptions := []presenceactor.Option{
		presenceactor.WithArchive(stores.memory),
		presenceactor.WithIdleTTL(textutil.ParseDurationOr(cfg.Runtime.ActorIdleTTL, 30*time.Minute)),
	}
	if stateStore, ok := stores.memory.(presenceactor.WorkingMemoryStore); ok {
		actorOptions = append(actorOptions, presenceactor.WithStateStore(stateStore))
	}
	presenceManager := presenceactor.NewManager(eventLog, actorOptions...)
	contextService.WithWorkingMemory(presenceManager)
	durableOutbox := outboxruntime.New(context.WithoutCancel(ctx), stores.outbox, outboxruntime.Config{
		WorkerCount: cfg.Runtime.WorkerCount,
	})

	// memorySvc：有向量存储时注入 WithVectorStore；同时注入差异化 TTL 配置
	memOpts := []memsvc.Option{}
	if vectorMemoryStore != nil {
		memOpts = append(memOpts, memsvc.WithVectorStore(vectorMemoryStore))
	}
	if len(cfg.Memory.TypeTTL) > 0 || cfg.Memory.DefaultTTL != "" {
		memOpts = append(memOpts, memsvc.WithTypeTTL(cfg.Memory.TypeTTL, cfg.Memory.DefaultTTL))
	}
	memOpts = append(memOpts, memsvc.WithOutbox(durableOutbox))
	if atomicStore, ok := stores.memory.(ports.AtomicMemoryProjectionStore); ok {
		memOpts = append(memOpts, memsvc.WithAtomicProjectionStore(atomicStore))
	}
	memorySvc := memsvc.New(stores.memory, memOpts...)
	if err := durableOutbox.Register("memory_vector_index", func(jobCtx context.Context, payload []byte) error {
		var record memorydomain.MemoryRecord
		if err := json.Unmarshal(payload, &record); err != nil {
			return fmt.Errorf("decode memory vector task: %w", err)
		}
		return memorySvc.ProcessVectorIndex(jobCtx, record)
	}); err != nil {
		_ = durableOutbox.Close()
		_ = stores.Close()
		return nil, fmt.Errorf("register memory outbox handler: %w", err)
	}
	memeOpts := []memesvc.Option{
		memesvc.WithVectorStore(memeVectorStore),
		memesvc.WithRetriever(hybridRetrieval),
		memesvc.WithOutbox(durableOutbox),
	}
	memeService := memesvc.New(stores.meme, cfg.Meme, memeOpts...)
	if err := durableOutbox.Register("meme_vector_index", func(jobCtx context.Context, payload []byte) error {
		var task memesvc.VectorIndexTask
		if err := json.Unmarshal(payload, &task); err != nil {
			return fmt.Errorf("decode meme vector task: %w", err)
		}
		return memeService.ProcessVectorIndex(jobCtx, task)
	}); err != nil {
		_ = durableOutbox.Close()
		_ = stores.Close()
		return nil, fmt.Errorf("register meme outbox handler: %w", err)
	}
	visionService := multimodalsvc.New(modelFactory, cfg.Multimodal)
	perceptionPipeline := presenceperception.New(visionService, memeService, presenceManager, presenceperception.WithOutbox(durableOutbox))
	if err := durableOutbox.Register("perception_event", func(jobCtx context.Context, payload []byte) error {
		var record presencedomain.EventRecord
		if err := json.Unmarshal(payload, &record); err != nil {
			return fmt.Errorf("decode perception event: %w", err)
		}
		return perceptionPipeline.Process(jobCtx, record)
	}); err != nil {
		_ = durableOutbox.Close()
		_ = stores.Close()
		return nil, fmt.Errorf("register perception outbox handler: %w", err)
	}

	mcpTools, err := toolsvc.ConnectMCP(ctx, cfg.Tools.MCPServers)
	if err != nil {
		_ = durableOutbox.Close()
		_ = stores.Close()
		return nil, err
	}
	mcpOwned := true
	defer func() {
		if mcpOwned {
			_ = mcpTools.Close()
		}
	}()
	writeApprovals := toolsvc.NewWriteApprovalStore(10 * time.Minute)
	toolRuntime := toolsvc.NewRuntime(stores.meme,
		toolsvc.WithProfileStore(stores.profile),
		toolsvc.WithPersonaID(cfg.Persona.ID),
		toolsvc.WithPersonaFactStore(stores.personaFacts),
		toolsvc.WithPersonaFactAdmins(cfg.Persona.FactUpdateUserWhitelist),
		toolsvc.WithMemoryService(memorySvc),
		toolsvc.WithMemeService(memeService),
		toolsvc.WithMemoryRetriever(hybridRetrieval),
		toolsvc.WithWriteApprovalStore(writeApprovals),
	)
	if err := toolRuntime.RegisterTools(ctx, mcpTools.Tools...); err != nil {
		_ = mcpTools.Close()
		_ = durableOutbox.Close()
		_ = stores.Close()
		return nil, fmt.Errorf("register MCP tools: %w", err)
	}
	if codexTool := toolsvc.NewCodexToolWithApproval(cfg.Tools.Codex, writeApprovals, cfg.Tools.Codex.WriteUserWhitelist); codexTool != nil {
		if err := toolRuntime.RegisterTools(ctx, codexTool); err != nil {
			_ = mcpTools.Close()
			_ = durableOutbox.Close()
			_ = stores.Close()
			return nil, fmt.Errorf("register Codex tool: %w", err)
		}
	}
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
		actionsvc.WithPresenceObserver(presenceManager),
		actionsvc.WithSelfID(cfg.QQ.SelfID),
		actionsvc.WithOutbox(durableOutbox),
	}
	executor := actionsvc.New(sender, memeService, guard, actionOpts...)
	if err := durableOutbox.Register("meme_mark_sent", func(jobCtx context.Context, payload []byte) error {
		var task actionsvc.MarkMemeSentTask
		if err := json.Unmarshal(payload, &task); err != nil {
			return fmt.Errorf("decode meme mark-sent task: %w", err)
		}
		return memeService.MarkSent(jobCtx, task.MemeID)
	}); err != nil {
		_ = durableOutbox.Close()
		_ = stores.Close()
		return nil, fmt.Errorf("register meme sent outbox handler: %w", err)
	}

	// F2 PersonaService：情绪状态动态驱动
	moodSvc := personasvc.New(stores.state, cfg.Persona.ID)
	turnObserver := presencereflection.New(stores.state, moodSvc, time.Duration(cfg.Autonomy.MinReplyIntervalSec)*time.Second, policyService)

	// Human Presence Runtime owns ingress, per-group working memory, candidate
	// scheduling, deliberation, realization, and outbound self-observation.
	// Context projection and planner compatibility remain behind this adapter.
	deliberator := presencedeliberation.NewAdapter(contextService, planner)
	jobTimeout := 120 * time.Second
	if cfg.Tools.Codex.Enabled {
		jobTimeout = max(jobTimeout, textutil.ParseDurationOr(cfg.Tools.Codex.Timeout, jobTimeout))
	}
	humanRuntime := presenceruntime.New(ctx, normalizer, presenceManager, deliberator, perceptionPipeline, turnObserver, executor, presenceruntime.Config{
		GroupWhitelist:    cfg.QQ.GroupWhitelist,
		SelfID:            cfg.QQ.SelfID,
		JobTimeout:        jobTimeout,
		ProactiveInterval: time.Minute,
		// 冷场主动开口：消费 autonomy 配置里的基础概率与评分阈值。
		ProactiveBaseProbability: cfg.Autonomy.ProactiveBaseProbability,
		ProactiveScoreThreshold:  cfg.Autonomy.ProactiveScoreThreshold,
	})
	humanRuntime.SetConfirmationObserver(writeApprovals)
	if thoughtStore, ok := stores.memory.(ports.ThoughtStore); ok {
		humanRuntime.SetThoughtStore(thoughtStore)
		contextService.WithThoughtStore(thoughtStore)
	}
	humanRuntime.SetMemoryStore(stores.memory)

	// Scheduler：注册所有定时任务
	sched := scheduler.New()
	moodSvc.RegisterJobs(sched, cfg.QQ.GroupWhitelist)

	// learning service：接入运行时，每 6 小时对白名单群跑一次增量学习
	profileService := profilesvc.New(stores.profile, cfg.Persona.ID)
	// curator_turn：把一轮对话的短文本亮点（≤24 字）写入长期记忆。
	if err := durableOutbox.Register("curator_turn", func(jobCtx context.Context, payload []byte) error {
		var snapshot conversationdomain.ContextSnapshot
		if err := json.Unmarshal(payload, &snapshot); err != nil {
			return fmt.Errorf("decode curator snapshot: %w", err)
		}
		text := strings.TrimSpace(snapshot.Event.Text)
		if text == "" || len([]rune(text)) > 24 {
			return nil
		}
		intent := memsvc.WriteIntent{
			Scope:         fmt.Sprintf("group:%d", snapshot.Event.GroupID),
			MemoryType:    "conversation_highlight",
			Subject:       "event",
			Content:       text,
			SourceEventID: snapshot.Event.EventID,
			Importance:    0.7,
			Confidence:    0.8,
		}
		_, err := memorySvc.MarkIntent(jobCtx, intent)
		return err
	}); err != nil {
		_ = durableOutbox.Close()
		_ = stores.Close()
		return nil, fmt.Errorf("register curator outbox handler: %w", err)
	}
	humanRuntime.AddEventObserver(profileService.ObserveEvent)
	humanRuntime.AddCompletedTurnObserver(presenceruntime.CompletedTurnObserverFunc(func(turnCtx context.Context, snapshot conversationdomain.ContextSnapshot, _ replydomain.ActionReceipt) error {
		payload, err := json.Marshal(snapshot)
		if err != nil {
			return fmt.Errorf("encode curator snapshot: %w", err)
		}
		key := snapshot.SnapshotID
		if key == "" {
			key = snapshot.Event.EventID
		}
		return durableOutbox.Enqueue(turnCtx, "curator_turn", key, payload)
	}))
	learningSvc, learnErr := learningsvc.New(ctx, stores.memory, stores.learning, memorySvc, learningsvc.WithOutbox(durableOutbox))
	if learnErr != nil {
		_ = durableOutbox.Close()
		_ = stores.Close()
		return nil, fmt.Errorf("learning service init: %w", learnErr)
	}
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
		_ = stores.Close()
		return nil, fmt.Errorf("register learning outbox handler: %w", err)
	}
	learningSvc.RegisterJobs(sched, cfg.QQ.GroupWhitelist)

	app := &App{
		cfg:          cfg,
		humanRuntime: humanRuntime,
		sched:        sched,
		cleanup: func() error {
			return errors.Join(durableOutbox.Close(), humanRuntime.Close(), presenceManager.Close(), mcpTools.Close(), stores.Close())
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
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	mcpOwned = false
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

func (a *App) ProcessRawEvent(ctx context.Context, payload []byte) (presenceruntime.Outcome, error) {
	return a.humanRuntime.ProcessRawEvent(ctx, payload)
}

func (a *App) Close() error {
	a.closeOnce.Do(func() {
		if a.sched != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			a.closeErr = errors.Join(a.closeErr, a.sched.Close(ctx))
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

