package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/runtime/bootstrap"
)

func main() {
	var (
		configPath = flag.String("config", "configs/config.yaml", "path to config file")
		onceEvent  = flag.String("once-event", "", "path to a single OneBot/NapCat event payload to process")
	)
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	app, err := bootstrap.NewApp(ctx, cfg)
	if err != nil {
		log.Fatalf("bootstrap app: %v", err)
	}
	defer func() {
		if err := app.Close(); err != nil {
			log.Printf("close app resources: %v", err)
		}
	}()

	if *onceEvent != "" {
		payload, err := os.ReadFile(*onceEvent)
		if err != nil {
			log.Fatalf("read once-event payload: %v", err)
		}

		result, err := app.ProcessRawEvent(ctx, payload)
		if err != nil {
			log.Fatalf("process once-event payload: %v", err)
		}

		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			log.Fatalf("marshal once-event result: %v", err)
		}

		fmt.Println(string(data))
		return
	}

	if err := app.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("run app: %v", err)
	}
}
