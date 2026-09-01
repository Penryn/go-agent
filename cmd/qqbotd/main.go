package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/phlin/go-agent/internal/app"
	"github.com/phlin/go-agent/internal/config"
)

func main() {
	// 启动阶段先用 TextHandler，加载配置后按 mode 重新初始化。
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	var (
		configPath = flag.String("config", "configs/config.yaml", "path to config file")
		onceEvent  = flag.String("once-event", "", "path to a single OneBot/NapCat event payload to process")
	)
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	// 按运行模式重新设置 handler：prod 输出 JSON，dev 输出易读 text。
	logLevel := parseLogLevel(cfg.App.LogLevel)
	if cfg.App.Mode == "prod" {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))
	} else {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))
	}
	slog.Info("starting", "mode", cfg.App.Mode, "log_level", logLevel)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	application, err := app.New(ctx, cfg)
	if err != nil {
		slog.Error("bootstrap app", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := application.Close(); err != nil {
			slog.Warn("close app resources", "error", err)
		}
	}()

	if *onceEvent != "" {
		payload, err := os.ReadFile(*onceEvent)
		if err != nil {
			slog.Error("read once-event payload", "error", err)
			os.Exit(1)
		}

		result, err := application.ProcessRawEvent(ctx, payload)
		if err != nil {
			slog.Error("process once-event payload", "error", err)
			os.Exit(1)
		}

		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			slog.Error("marshal once-event result", "error", err)
			os.Exit(1)
		}

		fmt.Println(string(data))
		return
	}

	if err := application.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("run app", "error", err)
		os.Exit(1)
	}
}

func parseLogLevel(s string) slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.TrimSpace(s))); err != nil {
		return slog.LevelInfo
	}
	return level
}
