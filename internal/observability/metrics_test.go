package observability

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// scrape renders the registry to its Prometheus text form.
func scrape(t *testing.T, m *Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("scrape status = %d", rec.Code)
	}
	return rec.Body.String()
}

func TestMetricsRecordsREDWithBoundedLabels(t *testing.T) {
	m := NewMetrics()
	m.ObserveRequest("GET", "/api/v1/videos/:id", 200, 12*time.Millisecond)
	m.ObserveRequest("GET", "/api/v1/videos/:id", 200, 8*time.Millisecond)
	m.ObserveRequest("POST", "/api/v1/videos/:id/report", 422, 3*time.Millisecond)
	m.ObserveRequest("GET", "/api/v1/videos/:id", 500, 5*time.Millisecond)

	out := scrape(t, m)

	// The counter uses the route TEMPLATE and status CLASS as labels (bounded),
	// never the raw URL, an id, or the exact status code.
	if !strings.Contains(out, `vidra_http_requests_total{method="GET",route="/api/v1/videos/:id",status_class="2xx"} 2`) {
		t.Errorf("expected 2xx GET counter = 2\n%s", out)
	}
	if !strings.Contains(out, `vidra_http_requests_total{method="POST",route="/api/v1/videos/:id/report",status_class="4xx"} 1`) {
		t.Errorf("expected 4xx POST counter = 1\n%s", out)
	}
	if !strings.Contains(out, `vidra_http_requests_total{method="GET",route="/api/v1/videos/:id",status_class="5xx"} 1`) {
		t.Errorf("expected 5xx GET counter = 1\n%s", out)
	}
	// The duration histogram is present with the same label set.
	if !strings.Contains(out, `vidra_http_request_duration_seconds_count{method="GET",route="/api/v1/videos/:id",status_class="2xx"} 2`) {
		t.Errorf("expected duration histogram count = 2\n%s", out)
	}
	// No id or raw URL ever leaks into a label value.
	if strings.Contains(out, "route=\"/api/v1/videos/abc") || strings.Contains(out, "status_class=\"200\"") {
		t.Errorf("label cardinality leak (raw id/url or exact status)\n%s", out)
	}
}

func TestMetricsEmptyRouteFoldsToUnmatched(t *testing.T) {
	m := NewMetrics()
	m.ObserveRequest("GET", "", 404, time.Millisecond)
	out := scrape(t, m)
	if !strings.Contains(out, `route="unmatched"`) {
		t.Errorf("empty route should fold to \"unmatched\"\n%s", out)
	}
}

func TestQueueDepthGauge(t *testing.T) {
	m := NewMetrics()
	m.RegisterQueueDepthSource(func(context.Context) ([]QueueDepth, error) {
		return []QueueDepth{
			{Queue: "transcode_jobs", State: "pending", Count: 3},
			{Queue: "transcode_jobs", State: "failed", Count: 1},
		}, nil
	})
	out := scrape(t, m)
	if !strings.Contains(out, `vidra_queue_depth{queue="transcode_jobs",state="pending"} 3`) {
		t.Errorf("expected pending depth 3\n%s", out)
	}
	if !strings.Contains(out, `vidra_queue_depth{queue="transcode_jobs",state="failed"} 1`) {
		t.Errorf("expected failed depth 1\n%s", out)
	}
}

func TestQueueDepthGaugeSwallowsSourceError(t *testing.T) {
	m := NewMetrics()
	m.RegisterQueueDepthSource(func(context.Context) ([]QueueDepth, error) {
		return nil, errors.New("db down")
	})
	// A source error must not fail the scrape (keep observability healthy).
	out := scrape(t, m)
	if strings.Contains(out, "vidra_queue_depth{") {
		t.Errorf("no gauge samples expected on source error\n%s", out)
	}
}
