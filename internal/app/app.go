package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"

	inboundnapcat "github.com/phlin/go-agent/internal/adapters/inbound/napcat"
	"github.com/phlin/go-agent/internal/adapters/inmemory"
	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
	outboundnapcat "github.com/phlin/go-agent/internal/adapters/outbound/napcat"
	postgresstore "github.com/phlin/go-agent/internal/adapters/storage/postgres"
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
	"github.com/phlin/go-agent/internal/application/ports"
	presenceruntime "github.com/phlin/go-agent/internal/application/presence"
	presencedeliberation "github.com/phlin/go-agent/internal/application/presence/deliberation"
	presencefeedback "github.com/phlin/go-agent/internal/application/presence/feedback"
	presenceactor "github.com/phlin/go-agent/internal/application/presence/group_actor"
	presenceingress "github.com/phlin/go-agent/internal/application/presence/ingress"
	presenceperception "github.com/phlin/go-agent/internal/application/presence/perception"
	planning "github.com/phlin/go-agent/internal/application/presence/planning"
	presencereflection "github.com/phlin/go-agent/internal/application/presence/reflection"
	profilesvc "github.com/phlin/go-agent/internal/application/profile"
	promptingsvc "github.com/phlin/go-agent/internal/application/prompting"
	reflectionsvc "github.com/phlin/go-agent/internal/application/reflection"
	relationshipsvc "github.com/phlin/go-agent/internal/application/relationship"
	retrievalsvc "github.com/phlin/go-agent/internal/application/retrieval"
	outboxruntime "github.com/phlin/go-agent/internal/application/runtime/outbox"
	"github.com/phlin/go-agent/internal/application/runtime/scheduler"
	scenesvc "github.com/phlin/go-agent/internal/application/scene"
	socialdecisionsvc "github.com/phlin/go-agent/internal/application/socialdecision"
	"github.com/phlin/go-agent/internal/application/textutil"
	toolsvc "github.com/phlin/go-agent/internal/application/tools"
	"github.com/phlin/go-agent/internal/config"
	conversationdomain "github.com/phlin/go-agent/internal/domain/conversation"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
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
	runtimeMCPServers, err := loadRuntimeMCPConfig(ctx, stores.db, cfg.Tools.MCPServers)
	if err != nil {
		_ = stores.Close()
		return nil, err
	}
	personaDefinition, err := personadomain.Compile(cfg.Persona)
	if err != nil {
		_ = stores.Close()
		return nil, fmt.Errorf("compile persona definition: %w", err)
	}

	var sender ports.OutboundSender = inmemory.NewSender()
	if cfg.QQ.Enabled && cfg.QQ.OutboundURL != "" {
		sender = outboundnapcat.NewSender(cfg.QQ.OutboundURL, cfg.QQ.OutboundToken, nil)
	}

	// 创建 ResponsePlanner 和 ResponseExecutor（依赖 sender 和 composer）
	composer := promptingsvc.NewComposer(cfg.Persona)

	// 配置 LLM（稍后在获得 modelFactory 后设置）
	// 配置在创建 hybridRetrieval 之后

	// 创建 composer 适配器以匹配 planning.TextComposer 接口
	composerAdapter := &textComposerAdapter{composer: composer}

	responsePlanner := planning.NewResponsePlanner(cfg.Persona, composerAdapter)
	responseExecutor := planning.NewResponseExecutor(sender)

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

	// 配置 Composer 的 LLM 和 MemoryRetriever
	composer.WithLLM(&llmAdapter{factory: modelFactory}).
		WithMemoryRetriever(&memoryRetrieverAdapter{retrieval: hybridRetrieval})

	contextService := contextsvc.New(stores.memory, stores.profile, stores.state, policyService, cfg.Persona, hybridRetrieval, cfg.Memory.TopK)
	contextService.WithPersonaFactStore(stores.personaFacts)
	contextService.WithRelationshipStore(stores.relationships)
	contextService.WithSceneStore(stores.scenes)
	relationshipService := relationshipsvc.New(stores.relationships, cfg.Persona.ID)
	sceneService := scenesvc.New(stores.scenes)
	eventLog := presenceingress.NewMemoryEventLog()

	// 社交决策服务（需要在 presenceManager 之前创建）
	decisionEngine := socialdecisionsvc.NewDecisionEngine(
		&sceneStoreAdapter{stores.scenes},
		&relationshipStoreAdapter{stores.relationships},
		stores.posture,
		stores.ephemeral,
		socialdecisionsvc.DefaultDecisionConfig(),
	)

	personaAssembler := personasvc.NewContextAssembler(
		stores.posture,
		stores.ephemeral,
		&factStoreAdapter{stores.personaFacts},
	)

	eventStoreAdapted := &eventStoreAdapter{stores.memory}
	feedbackCollector := reflectionsvc.NewFeedbackCollector(
		eventStoreAdapted,
		reflectionsvc.NewFeedbackClassifier(eventStoreAdapted),
	)

	// 创建反馈窗口管理器
	feedbackWindowManager := presencefeedback.NewWindowManager(relationshipService)

	// presenceManager 配置选项（包含决策引擎依赖）
	actorOptions := []presenceactor.Option{
		presenceactor.WithArchive(stores.memory),
		presenceactor.WithIdleTTL(textutil.ParseDurationOr(cfg.Runtime.ActorIdleTTL, 30*time.Minute)),
		presenceactor.WithDecisionEngine(decisionEngine),
		presenceactor.WithPersonaAssembler(personaAssembler),
		presenceactor.WithFeedbackCollector(feedbackCollector),
		presenceactor.WithFeedbackWindowManager(feedbackWindowManager),
		presenceactor.WithResponsePlanner(responsePlanner),
		presenceactor.WithResponseExecutor(responseExecutor),
		presenceactor.WithPersonaID(cfg.Persona.ID),
	}
	if stateStore, ok := stores.memory.(presenceactor.WorkingMemoryStore); ok {
		actorOptions = append(actorOptions, presenceactor.WithStateStore(stateStore))
	}
	presenceManager := presenceactor.NewManager(eventLog, actorOptions...)
	contextService.WithWorkingMemory(presenceManager)
	durableOutbox := outboxruntime.New(context.WithoutCancel(ctx), stores.outbox, outboxruntime.Config{
		WorkerCount: cfg.Runtime.WorkerCount,
	})
	canonService := personasvc.NewCanonService(
		stores.personaFacts,
		personaDefinition,
		modelFactory,
		personasvc.WithCanonOutbox(durableOutbox),
	)
	if err := durableOutbox.Register(personasvc.CanonExtractionTaskKind(), func(jobCtx context.Context, payload []byte) error {
		var task personasvc.CanonExtractionTask
		if err := json.Unmarshal(payload, &task); err != nil {
			return fmt.Errorf("decode persona canon extraction task: %w", err)
		}
		return canonService.ProcessExtraction(jobCtx, task)
	}); err != nil {
		_ = durableOutbox.Close()
		_ = stores.Close()
		return nil, fmt.Errorf("register persona canon outbox handler: %w", err)
	}
	if err := durableOutbox.Register(personasvc.CanonFinalizeTaskKind(), func(jobCtx context.Context, payload []byte) error {
		var task personasvc.CanonFinalizeTask
		if err := json.Unmarshal(payload, &task); err != nil {
			return fmt.Errorf("decode persona canon finalize task: %w", err)
		}
		return canonService.ProcessFinalize(jobCtx, task)
	}); err != nil {
		_ = durableOutbox.Close()
		_ = stores.Close()
		return nil, fmt.Errorf("register persona canon finalize handler: %w", err)
	}

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

	mcpTools, err := toolsvc.ConnectMCP(ctx, runtimeMCPServers)
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
		toolsvc.WithPersonaDefinition(personaDefinition),
		toolsvc.WithPersonaFactStore(stores.personaFacts),
		toolsvc.WithMemoryClaimService(memsvc.NewClaimService(stores.claims)),
		toolsvc.WithRelationshipService(relationshipService),
		toolsvc.WithPersonaFactAdmins(cfg.Persona.FactUpdateUserWhitelist),
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
	mcpManager := toolsvc.NewMCPManager(toolRuntime, mcpTools, runtimeMCPServers)
	if codexTool := toolsvc.NewCodexToolWithApproval(cfg.Tools.Codex, writeApprovals, cfg.Tools.Codex.WriteUserWhitelist); codexTool != nil {
		if err := toolRuntime.RegisterTools(ctx, codexTool); err != nil {
			_ = mcpTools.Close()
			_ = durableOutbox.Close()
			_ = stores.Close()
			return nil, fmt.Errorf("register Codex tool: %w", err)
		}
	}
	fallbackPlanner := promptingsvc.NewDeterministicPlanner(cfg.Persona)
	_ = promptingsvc.NewAgentPlanner(
		modelFactory,
		toolRuntime,
		promptingsvc.NewComposer(cfg.Persona),
		fallbackPlanner,
		presenceManager,
	)
	normalizer := normalizersvc.New("onebot", cfg.QQ.SelfID, cfg.Persona.Aliases)

	// F1 OutputGuard：从 persona 配置读取截断阈值
	guard := outputguardsvc.New(cfg.Persona.ReplyMaxChars*2, cfg.Persona.ReplyMaxSentences+1)
	actionOpts := []actionsvc.Option{
		actionsvc.WithPresenceObserver(presenceManager),
		actionsvc.WithEventObserver(sceneService.ObserveEvent),
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
	turnObserver.SetRelationshipService(relationshipService)

	// Human Presence Runtime owns ingress, per-group working memory, candidate
	// scheduling, deliberation, realization, and outbound self-observation.
	// 注意：deliberation 现在由 group_actor 中的决策引擎处理
	// 这里使用一个空的 deliberator 以保持接口兼容
	deliberator := &noOpDeliberator{}
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
	humanRuntime.SetCanonService(canonService)
	if thoughtStore, ok := stores.memory.(ports.ThoughtStore); ok {
		humanRuntime.SetThoughtStore(thoughtStore)
		contextService.WithThoughtStore(thoughtStore)
	}
	humanRuntime.SetMemoryRetriever(hybridRetrieval)

	// Scheduler：注册所有定时任务
	sched := scheduler.New()
	moodSvc.RegisterJobs(sched, cfg.QQ.GroupWhitelist)

	// learning service：接入运行时，每 6 小时对白名单群跑一次增量学习
	profileService := profilesvc.New(stores.profile)
	humanRuntime.AddEventObserver(profileService.ObserveEvent)
	humanRuntime.AddEventObserver(sceneService.ObserveEvent)
	humanRuntime.AddEventObserver(relationshipService.ObserveInbound)
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
	if retentionDays := cfg.Storage.Postgres.ObservabilityRetentionDays; retentionDays > 0 {
		prune := func(jobCtx context.Context) error {
			return postgresstore.PruneObservability(jobCtx, stores.db, retentionDays)
		}
		if err := prune(ctx); err != nil {
			slog.Warn("app: observability cleanup failed", "error", err)
		}
		sched.Register("observability-retention", 24*time.Hour, prune)
	}

	app := &App{
		cfg:          cfg,
		humanRuntime: humanRuntime,
		sched:        sched,
		cleanup: func() error {
			return errors.Join(durableOutbox.Close(), humanRuntime.Close(), presenceManager.Close(), mcpManager.Close(), stores.Close())
		},
		healthCheck: stores.HealthCheck,
	}
	if cfg.QQ.Enabled && cfg.QQ.EventWSURL != "" {
		app.inbound = inboundnapcat.NewWSReceiver(cfg.QQ.EventWSURL, cfg.QQ.EventWSToken)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", app.handleHealth)
	mainModelReady := strings.TrimSpace(cfg.Models.Main.APIKey) != "" && strings.TrimSpace(cfg.Models.Main.Model) != ""
	vectorSearchReady := vectorGraph.memory != nil
	health := newCapabilityHealth(mainModelReady, vectorSearchReady)
	adminHandler := newAdminHandler(stores.db, stores.state, stores.personaFacts, personaDefinition, cfg, app.qqConnected, mcpManager, mainModelReady, vectorSearchReady, health)
	probeInterval := textutil.ParseDurationOr(cfg.Models.HealthProbeInterval, 0)
	if probeInterval > 0 && (mainModelReady || vectorSearchReady) {
		sched.Register("provider-health-probe", probeInterval, func(jobCtx context.Context) error {
			return probeProviders(jobCtx, modelFactory, health, mainModelReady, vectorSearchReady)
		})
	}
	mux.Handle("/admin", adminHandler)
	mux.Handle("/admin/", adminHandler)

	app.server = &http.Server{
		Addr:         cfg.Server.HTTPListen,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	mcpOwned = false
	return app, nil
}

func (a *App) qqConnected() bool { return a.inbound != nil && a.inbound.Connected() }

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

// textComposerAdapter 适配 prompting.Composer 到 planning.TextComposer 接口
type textComposerAdapter struct {
	composer *promptingsvc.Composer
}

func (a *textComposerAdapter) ComposeResponse(
	ctx context.Context,
	personaCtx *personadomain.PersonaContext,
	evt *conversationdomain.ConversationEvent,
	intent string,
) (string, error) {
	// 调用 Composer 的 ComposeResponse 方法
	return a.composer.ComposeResponse(ctx, personaCtx, evt, intent)
}

// llmAdapter 适配 modelFactory 到 prompting.LLMCaller 接口
type llmAdapter struct {
	factory *modeladapter.Factory
}

func (a *llmAdapter) Generate(ctx context.Context, prompt string) (string, error) {
	if a.factory == nil {
		return "", fmt.Errorf("model factory not available")
	}

	model, err := a.factory.MainChatModel(ctx)
	if err != nil {
		return "", fmt.Errorf("get main model: %w", err)
	}

	response, err := model.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: prompt},
	})
	if err != nil {
		return "", fmt.Errorf("generate: %w", err)
	}

	if response == nil || response.Content == "" {
		return "", fmt.Errorf("empty response")
	}

	return response.Content, nil
}

// memoryRetrieverAdapter 适配 retrievalsvc.Service 到 prompting.MemoryRetriever 接口
type memoryRetrieverAdapter struct {
	retrieval *retrievalsvc.Service
}

func (a *memoryRetrieverAdapter) RetrieveRelevant(ctx context.Context, groupID int64, query string, limit int) ([]memorydomain.MemoryRecord, error) {
	if a.retrieval == nil {
		return nil, nil
	}

	memories, err := a.retrieval.SearchMemories(ctx, ports.MemoryQuery{
		GroupID: groupID,
		Query:   query,
		TopK:    limit,
	})
	if err != nil {
		return nil, err
	}

	return memories, nil
}

// noOpDeliberator 是一个空的 deliberator 实现
// 实际的决策逻辑现在在 group_actor 的决策引擎中处理
type noOpDeliberator struct{}

func (n *noOpDeliberator) Deliberate(ctx context.Context, input presencedeliberation.Input) (presencedeliberation.Result, error) {
	// 返回空结果，因为决策现在由 decideAndRespond 处理
	return presencedeliberation.Result{}, nil
}
