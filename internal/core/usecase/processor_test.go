package usecase_test

import (
	"context"
	"os"
	"testing"

	"github.com/phlin/go-agent/internal/config"
	"github.com/phlin/go-agent/internal/runtime/bootstrap"
)

func TestProcessorMainFlow(t *testing.T) {
	cfg := config.Default()
	cfg.App.Mode = "test"
	cfg.DefaultPolicy.QuietHours = nil
	cfg.QQ.SelfID = 123456

	app, err := bootstrap.NewApp(context.Background(), cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	payload, err := os.ReadFile("../../../tests/testdata/mention_event.json")
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}

	result, err := app.ProcessRawEvent(context.Background(), payload)
	if err != nil {
		t.Fatalf("process raw event: %v", err)
	}

	if result.Decision.Action != "reply" {
		t.Fatalf("unexpected decision action: %s", result.Decision.Action)
	}
	if !result.Receipt.Sent {
		t.Fatalf("expected sent receipt")
	}
	if len(result.Plan.Bubbles) == 0 {
		t.Fatalf("expected planned bubbles")
	}
}
