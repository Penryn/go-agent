package app

import (
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/app/admin"
)

func TestCapabilityHealthTracksProbeResult(t *testing.T) {
	health := admin.NewCapabilityHealth(true, true)
	when := time.Date(2026, 9, 3, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	health.RecordMainModel(false, when) // false indicates error
	health.RecordVector(true, when)     // true indicates success
	mainStatus, vectorStatus, mainChecked, vectorChecked := health.Snapshot()
	if mainStatus != "degraded" || vectorStatus != "ready" {
		t.Fatalf("unexpected statuses: %s/%s", mainStatus, vectorStatus)
	}
	if mainChecked == nil || vectorChecked == nil || !mainChecked.Equal(when) || !vectorChecked.Equal(when) {
		t.Fatalf("unexpected check times: %v/%v", mainChecked, vectorChecked)
	}
}
