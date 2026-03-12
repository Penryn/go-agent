package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	inboundnapcat "github.com/phlin/go-agent/internal/adapters/inbound/napcat"
	"github.com/phlin/go-agent/internal/adapters/inmemory"
	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
	outboundnapcat "github.com/phlin/go-agent/internal/adapters/outbound/napcat"
	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/core/ports"
	"github.com/phlin/go-agent/internal/core/usecase"
	actionsvc "github.com/phlin/go-agent/internal/services/action"
	autonomysvc "github.com/phlin/go-agent/internal/services/autonomy"
	contextsvc "github.com/phlin/go-agent/internal/services/context"
	gatesvc "github.com/phlin/go-agent/internal/services/gate"
	memesvc "github.com/phlin/go-agent/internal/services/meme"
	memsvc "github.com/phlin/go-agent/internal/services/memory"
	multimodalsvc "github.com/phlin/go-agent/internal/services/multimodal"
	normalizersvc "github.com/phlin/go-agent/internal/services/normalizer"
	policysvc "github.com/phlin/go-agent/internal/services/policy"
	profilesvc "github.com/phlin/go-agent/internal/services/profile"
	promptingsvc "github.com/phlin/go-agent/internal/services/prompting"
	toolsvc "github.com/phlin/go-agent/internal/services/tools"
)

type App struct {
	cfg         config.Config
	processor   *usecase.Processor
	inbound     ports.InboundSource
	server      *http.Server
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
	contextService := contextsvc.New(stores.memory, stores.profile, stores.state, policyService, cfg.Persona)
	modelFactory := modeladapter.NewFactory(cfg.Models)
	if err := modelFactory.Warmup(ctx); err != nil {
		_ = stores.Close()
		return nil, err
	}
	gateService := gatesvc.New(modelFactory)
	autonomyService := autonomysvc.New(policyService, gateService)
	visionService := multimodalsvc.New(modelFactory)
	toolRuntime := toolsvc.NewRuntime(stores.memory, stores.meme, toolsvc.WithProfileStore(stores.profile))
	fallbackPlanner := promptingsvc.NewDeterministicPlanner(cfg.Persona)
	planner := promptingsvc.NewAgentPlanner(
		modelFactory,
		toolRuntime,
		promptingsvc.NewComposer(cfg.Persona),
		fallbackPlanner,
	)
	normalizer := normalizersvc.New("onebot", cfg.QQ.SelfID, cfg.Persona.Aliases)
	memeService := memesvc.New(stores.meme)
	executor := actionsvc.New(sender, memeService)
	processor := usecase.NewProcessor(
		normalizer,
		contextService,
		autonomyService,
		planner,
		executor,
		stores.memory,
		stores.state,
		memsvc.New(stores.memory),
		profilesvc.New(stores.profile),
		memeService,
		visionService,
		cfg.QQ.GroupWhitelist,
	)

	app := &App{
		cfg:         cfg,
		processor:   processor,
		cleanup:     stores.Close,
		healthCheck: stores.HealthCheck,
	}
	if cfg.QQ.Enabled && cfg.QQ.EventWSURL != "" {
		app.inbound = inboundnapcat.NewWSReceiver(cfg.QQ.EventWSURL, cfg.QQ.AccessToken, nil)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", app.handleHealth)
	mux.Handle(cfg.QQ.InboundRoute, inboundnapcat.NewHandler(cfg.QQ.AccessToken, func(ctx context.Context, payload []byte) (any, error) {
		return app.processor.ProcessRawEvent(ctx, payload)
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
	errCh := make(chan error, 1)
	go func() {
		if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	if a.inbound != nil {
		go func() {
			if err := a.inbound.Receive(ctx, func(inner context.Context, payload []byte) error {
				if _, err := a.processor.ProcessRawEvent(inner, payload); err != nil {
					log.Printf("process inbound event: %v", err)
				}
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

func (a *App) ProcessRawEvent(ctx context.Context, payload []byte) (usecase.ProcessResult, error) {
	return a.processor.ProcessRawEvent(ctx, payload)
}

func (a *App) Close() error {
	a.closeOnce.Do(func() {
		if a.cleanup != nil {
			a.closeErr = a.cleanup()
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
