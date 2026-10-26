package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/phlin/go-agent/internal/adapters/inmemory"
	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
	outboundnapcat "github.com/phlin/go-agent/internal/adapters/outbound/napcat"
	postgresstore "github.com/phlin/go-agent/internal/adapters/storage/postgres"
	actionsvc "github.com/phlin/go-agent/internal/application/action"
	contextsvc "github.com/phlin/go-agent/internal/application/context"
	learningsvc "github.com/phlin/go-agent/internal/application/learning"
	memesvc "github.com/phlin/go-agent/internal/application/meme"
	memsvc "github.com/phlin/go-agent/internal/application/memory"
	"github.com/phlin/go-agent/internal/application/modelusage"
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
	presenceperception "github.com/phlin/go-agent/internal/application/presence/perception"
	presencereflection "github.com/phlin/go-agent/internal/application/presence/reflection"
	profilesvc "github.com/phlin/go-agent/internal/application/profile"
	promptingsvc "github.com/phlin/go-agent/internal/application/prompting"
	relationshipsvc "github.com/phlin/go-agent/internal/application/relationship"
	retrievalsvc "github.com/phlin/go-agent/internal/application/retrieval"
	outboxruntime "github.com/phlin/go-agent/internal/application/runtime/outbox"
	"github.com/phlin/go-agent/internal/application/runtime/scheduler"
	scenesvc "github.com/phlin/go-agent/internal/application/scene"
	textutil "github.com/phlin/go-agent/internal/application/textutil"
	toolsvc "github.com/phlin/go-agent/internal/application/tools"
	workspaceapp "github.com/phlin/go-agent/internal/application/workspace"
	"github.com/phlin/go-agent/internal/config"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
	personadomain "github.com/phlin/go-agent/internal/domain/persona"
	presencedomain "github.com/phlin/go-agent/internal/domain/presence"
)

type runtimeGraph struct {
	presenceRuntime   *presenceruntime.Runtime
	sched             *scheduler.Scheduler
	mcpManager        *toolsvc.MCPManager
	modelFactory      *modeladapter.Factory
	personaDefinition personadomain.PersonaDefinition
	mainModelReady    bool
	vectorSearchReady bool
	cleanup           func() error
	healthCheck       func(context.Context) error
}

// buildRuntimeGraph wires the application use cases in dependency order. The
// composition root owns this lifecycle boundary; callers only receive a
// ready-to-run graph or a fully cleaned-up error.
func buildRuntimeGraph(ctx context.Context, cfg config.Config, stores *storeBundle) (graph *runtimeGraph, buildErr error) {
	var (
		presenceManager *presenceactor.Manager
		durableOutbox   *outboxruntime.Runtime
		mcpTools        *toolsvc.MCPTools
		mcpManager      *toolsvc.MCPManager
		presenceRuntime *presenceruntime.Runtime
	)
	// Stop producers before the services they use. The same ownership order
	// applies to incomplete construction and normal shutdown.
	cleanup := func() error {
		var errs []error
		if presenceRuntime != nil {
			errs = append(errs, presenceRuntime.Close())
		}
		if durableOutbox != nil {
			errs = append(errs, durableOutbox.Close())
		}
		if presenceManager != nil {
			errs = append(errs, presenceManager.Close())
		}
		if mcpManager != nil {
			errs = append(errs, mcpManager.Close())
		} else if mcpTools != nil {
			errs = append(errs, mcpTools.Close())
		}
		return errors.Join(append(errs, stores.Close())...)
	}
	defer func() {
		if buildErr != nil {
			buildErr = errors.Join(buildErr, cleanup())
		}
	}()

	runtimeMCPServers, err := loadRuntimeMCPConfig(ctx, stores.db, cfg.Tools.MCPServers)
	if err != nil {
		return nil, err
	}
	personaDefinition, err := personadomain.Compile(cfg.Persona)
	if err != nil {
		return nil, fmt.Errorf("compile persona definition: %w", err)
	}

	var sender ports.OutboundSender = inmemory.NewSender()
	if cfg.QQ.Enabled && cfg.QQ.OutboundURL != "" {
		sender = outboundnapcat.NewSender(cfg.QQ.OutboundURL, cfg.QQ.OutboundToken, nil)
	}

	composer := promptingsvc.NewComposer(cfg.Persona)

	policyService := policysvc.New(cfg)
	modelFactory := modeladapter.NewFactory(cfg.Models)
	if err := modelFactory.Warmup(ctx); err != nil {
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
	contextService.WithRelationshipStore(stores.relationships)
	contextService.WithSceneStore(stores.scenes)
	relationshipService := relationshipsvc.New(stores.relationships, cfg.Persona.ID)
	sceneService := scenesvc.New(stores.scenes)

	actorOptions := []presenceactor.Option{
		presenceactor.WithArchive(stores.memory),
		presenceactor.WithIdleTTL(textutil.ParseDurationOr(cfg.Runtime.ActorIdleTTL, 30*time.Minute)),
	}
	if stateStore, ok := stores.memory.(presenceactor.WorkingMemoryStore); ok {
		actorOptions = append(actorOptions, presenceactor.WithStateStore(stateStore))
	}
	presenceManager = presenceactor.NewManager(actorOptions...)
	contextService.WithWorkingMemory(presenceManager)
	durableOutbox = outboxruntime.New(context.WithoutCancel(ctx), stores.outbox, outboxruntime.Config{
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
		return nil, fmt.Errorf("register persona canon outbox handler: %w", err)
	}
	if err := durableOutbox.Register(personasvc.CanonFinalizeTaskKind(), func(jobCtx context.Context, payload []byte) error {
		var task personasvc.CanonFinalizeTask
		if err := json.Unmarshal(payload, &task); err != nil {
			return fmt.Errorf("decode persona canon finalize task: %w", err)
		}
		return canonService.ProcessFinalize(jobCtx, task)
	}); err != nil {
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
	contextService.WithConstraints(stores.constraints)
	if err := durableOutbox.Register("memory_vector_index", func(jobCtx context.Context, payload []byte) error {
		var record memorydomain.MemoryRecord
		if err := json.Unmarshal(payload, &record); err != nil {
			return fmt.Errorf("decode memory vector task: %w", err)
		}
		return memorySvc.ProcessVectorIndex(jobCtx, record)
	}); err != nil {
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
		return nil, fmt.Errorf("register perception outbox handler: %w", err)
	}

	mcpTools, err = toolsvc.ConnectMCP(ctx, runtimeMCPServers)
	if err != nil {
		return nil, err
	}
	writeApprovals := toolsvc.NewWriteApprovalStore(10 * time.Minute)
	toolRuntime := toolsvc.NewRuntime(stores.meme,
		toolsvc.WithProfileStore(stores.profile),
		toolsvc.WithPersonaDefinition(personaDefinition),
		toolsvc.WithPersonaFactStore(stores.personaFacts),
		toolsvc.WithMemoryService(memorySvc),
		toolsvc.WithRelationshipService(relationshipService),
		toolsvc.WithPersonaFactAdmins(cfg.Persona.FactUpdateUserWhitelist),
		toolsvc.WithMemeService(memeService),
		toolsvc.WithMemoryRetriever(hybridRetrieval),
		toolsvc.WithWriteApprovalStore(writeApprovals),
	)
	if err := toolRuntime.RegisterTools(ctx, mcpTools.Tools...); err != nil {
		return nil, fmt.Errorf("register MCP tools: %w", err)
	}
	mcpManager = toolsvc.NewMCPManager(toolRuntime, mcpTools, runtimeMCPServers)
	if codexTool := toolsvc.NewCodexToolWithApproval(cfg.Tools.Codex, writeApprovals, cfg.Tools.Codex.WriteUserWhitelist); codexTool != nil {
		if err := toolRuntime.RegisterTools(ctx, codexTool); err != nil {
			return nil, fmt.Errorf("register Codex tool: %w", err)
		}
	}
	fallbackPlanner := promptingsvc.NewDeterministicPlanner(cfg.Persona)
	agentPlanner := promptingsvc.NewAgentPlanner(
		modelFactory,
		toolRuntime,
		composer,
		fallbackPlanner,
	)
	deliberator := presencedeliberation.NewAdapter(contextService, agentPlanner)
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
		return nil, fmt.Errorf("register meme sent outbox handler: %w", err)
	}

	// F2 PersonaService：情绪状态动态驱动
	moodSvc := personasvc.New(stores.state, cfg.Persona.ID)
	turnObserver := presencereflection.New(stores.state, moodSvc, time.Duration(cfg.Autonomy.MinReplyIntervalSec)*time.Second, policyService)
	turnObserver.SetRelationshipService(relationshipService)

	var workspaceManager *workspaceapp.Manager
	if cfg.Tools.Codex.Enabled {
		workspaceRoot := cfg.Tools.Codex.WorkspaceRoot
		if strings.TrimSpace(workspaceRoot) == "" {
			workspaceRoot = filepath.Join(os.TempDir(), "qqbot-codex-workspaces")
		}
		workspaceManager, err = workspaceapp.NewManager(workspaceRoot)
		if err != nil {
			return nil, fmt.Errorf("create Codex workspace manager: %w", err)
		}
	}
	profileService := profilesvc.New(stores.profile, cfg.QQ.SelfID)
	usageSink, _ := stores.memory.(modelusage.Sink)
	thoughtStore, _ := stores.memory.(ports.ThoughtStore)
	contextService.WithThoughtStore(thoughtStore)
	presenceRuntime = presenceruntime.New(ctx, presenceruntime.Dependencies{
		Normalizer:    normalizer,
		WorkingMemory: presenceManager,
		Deliberator:   deliberator,
		Perception:    perceptionPipeline,
		Turns:         turnObserver,
		Executor:      executor,
		Confirmations: writeApprovals,
		Workspaces:    workspaceManager,
		Canon:         canonService,
		Feedback:      presencefeedback.NewLifecycleObserver(presenceManager, relationshipService),
		ModelUsage:    usageSink,
		Thoughts:      thoughtStore,
		EventObservers: []presenceruntime.EventObserverFunc{
			profileService.ObserveEvent,
			sceneService.ObserveEvent,
			relationshipService.ObserveInbound,
		},
	}, presenceruntime.Config{GroupWhitelist: cfg.QQ.GroupWhitelist})

	sched := scheduler.New()
	moodSvc.RegisterJobs(sched, cfg.QQ.GroupWhitelist)
	// Incremental learning is an asynchronous group-friend capability.
	learningSvc, learnErr := learningsvc.New(ctx, stores.memory, stores.learning, memorySvc,
		learningsvc.WithOutbox(durableOutbox), learningsvc.WithLLM(&llmAdapter{factory: modelFactory}))
	if learnErr != nil {
		return nil, fmt.Errorf("learning service init: %w", learnErr)
	}
	if err := durableOutbox.Register("learning_extract", func(jobCtx context.Context, payload []byte) error {
		return learningSvc.ProcessTask(jobCtx, payload)
	}); err != nil {
		return nil, fmt.Errorf("register learning outbox handler: %w", err)
	}
	if err := durableOutbox.Start(); err != nil {
		return nil, fmt.Errorf("start durable outbox: %w", err)
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
	graph = &runtimeGraph{
		presenceRuntime:   presenceRuntime,
		sched:             sched,
		mcpManager:        mcpManager,
		modelFactory:      modelFactory,
		personaDefinition: personaDefinition,
		mainModelReady:    strings.TrimSpace(cfg.Models.Main.APIKey) != "" && strings.TrimSpace(cfg.Models.Main.Model) != "",
		vectorSearchReady: vectorGraph.memory != nil,
		healthCheck:       stores.HealthCheck,
		cleanup:           cleanup,
	}
	return graph, nil
}
