package app

import (
	"errors"
	"testing"
	"time"
)

func TestCapabilityHealthTracksProbeResult(t *testing.T) {
	health := newCapabilityHealth(true, true)
	when := time.Date(2026, 9, 3, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	health.updateMain(errors.New("timeout"), when)
	health.updateVector(nil, when)
	mainStatus, vectorStatus, mainChecked, vectorChecked := health.snapshot()
	if mainStatus != "degraded" || vectorStatus != "ready" {
		t.Fatalf("unexpected statuses: %s/%s", mainStatus, vectorStatus)
	}
	if mainChecked == nil || vectorChecked == nil || !mainChecked.Equal(when) || !vectorChecked.Equal(when) {
		t.Fatalf("unexpected check times: %v/%v", mainChecked, vectorChecked)
	}
}
