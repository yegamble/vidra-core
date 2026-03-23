package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsHandler_IncludesSchedulerMetrics(t *testing.T) {
	// Set scheduler metrics
	SetSchedulerConfig(true, 7, 4)
	SetSchedulerTick(time.Unix(1700000000, 0))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	Handler(rr, req)

	body := rr.Body.String()
	for _, substr := range []string{
		"vidra_scheduler_enabled",
		"vidra_scheduler_interval_seconds",
		"vidra_scheduler_burst",
		"vidra_scheduler_last_tick_unixtime",
	} {
		if !strings.Contains(body, substr) {
			t.Fatalf("expected metrics output to contain %q", substr)
		}
	}
}
