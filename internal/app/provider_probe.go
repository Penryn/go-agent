package app

import (
	"context"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
)

type capabilityHealth struct {
	mu            sync.RWMutex
	mainStatus    string
	vectorStatus  string
	mainChecked   *time.Time
	vectorChecked *time.Time
}

func newCapabilityHealth(mainReady, vectorReady bool) *capabilityHealth {
	mainStatus, vectorStatus := "not_configured", "disabled"
	if mainReady {
		mainStatus = "ready"
	}
	if vectorReady {
		vectorStatus = "idle"
	}
	return &capabilityHealth{mainStatus: mainStatus, vectorStatus: vectorStatus}
}

func (h *capabilityHealth) updateMain(err error, at time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.mainStatus = "ready"
	if err != nil {
		h.mainStatus = "degraded"
	}
	at = at.UTC()
	h.mainChecked = &at
}

func (h *capabilityHealth) updateVector(err error, at time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.vectorStatus = "ready"
	if err != nil {
		h.vectorStatus = "degraded"
	}
	at = at.UTC()
	h.vectorChecked = &at
}

func (h *capabilityHealth) snapshot() (string, string, *time.Time, *time.Time) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.mainStatus, h.vectorStatus, cloneTime(h.mainChecked), cloneTime(h.vectorChecked)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func probeProviders(ctx context.Context, factory *modeladapter.Factory, health *capabilityHealth, mainReady, vectorReady bool) error {
	var firstErr error
	if mainReady {
		probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		model, err := factory.MainChatModel(probeCtx)
		if err == nil {
			_, err = model.Generate(probeCtx, []*schema.Message{schema.UserMessage("health check")})
		}
		cancel()
		health.updateMain(err, time.Now())
		if err != nil {
			firstErr = err
		}
	}
	if vectorReady {
		probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		embedder, err := factory.EmbeddingModel(probeCtx)
		if err == nil {
			_, err = embedder.EmbedStrings(probeCtx, []string{"health check"})
		}
		cancel()
		health.updateVector(err, time.Now())
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
