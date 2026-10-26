package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/phlin/go-agent/internal/app"
	"github.com/phlin/go-agent/internal/config"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if err := run(); err != nil {
		slog.Error("qqbotd stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	path := flag.String("config", "configs/config.yaml", "configuration file")
	flag.Parse()
	cfg, err := config.Load(*path)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.App.LogLevel)); err != nil {
		return err
	}
	options := &slog.HandlerOptions{Level: level}
	if cfg.App.Mode == "prod" {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, options)))
	} else {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, options)))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	a, err := app.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer a.Close()
	err = a.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
