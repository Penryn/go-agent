package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/phlin/go-agent/internal/chat"
	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/meme"
	"github.com/phlin/go-agent/internal/memory"
	"github.com/phlin/go-agent/internal/model"
	"github.com/phlin/go-agent/internal/qq"
	"github.com/phlin/go-agent/internal/store"
)

type App struct {
	cfg      config.Config
	store    *store.Store
	runtime  *chat.Runtime
	memories *memory.Service
	memes    *meme.Service
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
	models := model.New(cfg.Models, cfg.ModelConcurrency)
	a.memories = &memory.Service{Store: db, Model: models, Embedding: models, TopK: cfg.Memory.TopK}
	a.memes = &meme.Service{Store: db, Model: models, Embedding: models, Config: cfg.Meme, HTTP: qq.MediaClient()}
	a.qq = &qq.Client{URL: cfg.QQ.OutboundURL, Token: cfg.QQ.OutboundToken, WSURL: cfg.QQ.EventWSURL, WSToken: cfg.QQ.EventWSToken}
	var sender chat.Sender
	if cfg.QQ.Enabled {
		if err := db.AcquireSender(ctx); err != nil {
			return fail(err)
		}
		sender = ownedSender{a.qq, db}
	}
	a.runtime = chat.New(child, &chat.Runtime{Store: db, Model: models, Memory: a.memories, Memes: a.memes, Sender: sender, Persona: cfg.Persona, Config: cfg.Chat, SelfID: cfg.QQ.SelfID, Groups: cfg.QQ.GroupWhitelist})
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
	return s.qq.Send(ctx, g, text, quote, path)
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
	memory.RunBackground(a.ctx, &a.wg, a.cfg.Memory.Interval, a.memories.Tick)
	memory.RunBackground(a.ctx, &a.wg, 3*time.Second, a.memes.Tick)
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
