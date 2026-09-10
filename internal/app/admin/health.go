package admin

import (
	"sync"
	"time"
)

type CapabilityHealth struct {
	mu                sync.RWMutex
	mainModelStatus   string
	vectorStatus      string
	mainModelChecked  *time.Time
	vectorChecked     *time.Time
}

func NewCapabilityHealth(mainReady, vectorReady bool) *CapabilityHealth {
	h := &CapabilityHealth{}
	if mainReady {
		h.mainModelStatus = "ready"
	} else {
		h.mainModelStatus = "not_configured"
	}
	if vectorReady {
		h.vectorStatus = "idle"
	} else {
		h.vectorStatus = "disabled"
	}
	return h
}

func (h *CapabilityHealth) RecordMainModel(ok bool, at time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ok {
		h.mainModelStatus = "ready"
	} else {
		h.mainModelStatus = "degraded"
	}
	h.mainModelChecked = &at
}

func (h *CapabilityHealth) RecordVector(ok bool, at time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ok {
		h.vectorStatus = "ready"
	} else {
		h.vectorStatus = "degraded"
	}
	h.vectorChecked = &at
}

func (h *CapabilityHealth) Snapshot() (string, string, *time.Time, *time.Time) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.mainModelStatus, h.vectorStatus, h.mainModelChecked, h.vectorChecked
}
