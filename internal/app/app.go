package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
	inboundnapcat "github.com/phlin/go-agent/internal/adapters/inbound/napcat"
	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
	"github.com/phlin/go-agent/internal/app/admin"
	"github.com/phlin/go-agent/internal/application/ports"
	presenceruntime "github.com/phlin/go-agent/internal/application/presence"
	retrievalsvc "github.com/phlin/go-agent/internal/application/retrieval"
	"github.com/phlin/go-agent/internal/application/runtime/scheduler"
	"github.com/phlin/go-agent/internal/application/textutil"
	"github.com/phlin/go-agent/internal/config"
	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
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
	graph, err := buildRuntimeGraph(ctx, cfg, stores)
	if err != nil {
		return nil, err
	}

	app := &App{
		cfg:          cfg,
		humanRuntime: graph.humanRuntime,
		sched:        graph.sched,
		cleanup:      graph.cleanup,
		healthCheck:  graph.healthCheck,
	}
	if cfg.QQ.Enabled && cfg.QQ.EventWSURL != "" {
		app.inbound = inboundnapcat.NewWSReceiver(cfg.QQ.EventWSURL, cfg.QQ.EventWSToken)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", app.handleHealth)
	health := admin.NewCapabilityHealth(graph.mainModelReady, graph.vectorSearchReady)
	adminHandler := admin.NewHandler(stores.db, stores.state, stores.personaFacts, graph.personaDefinition, cfg, app.qqConnected, graph.mcpManager, graph.mainModelReady, graph.vectorSearchReady, health, adminAssets)
	probeInterval := textutil.ParseDurationOr(cfg.Models.HealthProbeInterval, 0)
	if probeInterval > 0 && (graph.mainModelReady || graph.vectorSearchReady) {
		graph.sched.Register("provider-health-probe", probeInterval, func(jobCtx context.Context) error {
			return probeProviders(jobCtx, graph.modelFactory, health, graph.mainModelReady, graph.vectorSearchReady)
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

func (a *memoryRetrieverAdapter) RetrieveRelevant(ctx context.Context, query ports.MemoryQuery) ([]memorydomain.MemoryRecord, error) {
	if a.retrieval == nil {
		return nil, nil
	}

	memories, err := a.retrieval.SearchMemories(ctx, query)
	if err != nil {
		return nil, err
	}

	return memories, nil
}
