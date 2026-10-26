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
	if err := run(); err != nil {
		slog.Error("qqbotd stopped", "error", err)
		os.Exit(1)
	}
}

// run owns process resources. Returning errors here lets every cleanup run
// before main chooses an exit code.
func run() error {
	// 启动阶段先用 TextHandler，加载配置后按 mode 重新初始化。
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	var (
		configPath = flag.String("config", "configs/config.yaml", "path to config file")
		onceEvent  = flag.String("once-event", "", "path to a single OneBot/NapCat event payload to process")
	)
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
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
		return fmt.Errorf("bootstrap app: %w", err)
	}
	defer func() {
		if err := application.Close(); err != nil {
			slog.Warn("close app resources", "error", err)
		}
	}()

	if *onceEvent != "" {
		payload, err := os.ReadFile(*onceEvent)
		if err != nil {
			return fmt.Errorf("read once-event payload: %w", err)
		}

		result, err := application.ProcessRawEvent(ctx, payload)
		if err != nil {
			return fmt.Errorf("process once-event payload: %w", err)
		}

		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal once-event result: %w", err)
		}

		fmt.Println(string(data))
		return nil
	}

	if err := application.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("run app: %w", err)
	}
	return nil
}

func parseLogLevel(s string) slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.TrimSpace(s))); err != nil {
		return slog.LevelInfo
	}
	return level
}
