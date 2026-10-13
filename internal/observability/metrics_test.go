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

func TestJobQueueHealthGauges(t *testing.T) {
	m := NewMetrics()
	m.RegisterJobQueueHealthSource(func(context.Context) ([]JobQueueHealth, error) {
		return []JobQueueHealth{{
			Queue: "transcode_jobs", OldestQueuedAgeSeconds: 123, StaleRunning: 2,
		}}, nil
	})
	out := scrape(t, m)
	if !strings.Contains(out, `vidra_job_oldest_queued_age_seconds{queue="transcode_jobs"} 123`) {
		t.Errorf("expected queue age gauge\n%s", out)
	}
	if !strings.Contains(out, `vidra_job_stale_running{queue="transcode_jobs"} 2`) {
		t.Errorf("expected stale-running gauge\n%s", out)
	}
}

func TestJobQueueHealthGaugeSwallowsSourceError(t *testing.T) {
	m := NewMetrics()
	m.RegisterJobQueueHealthSource(func(context.Context) ([]JobQueueHealth, error) {
		return nil, errors.New("db down")
	})
	out := scrape(t, m)
	if strings.Contains(out, "vidra_job_oldest_queued_age_seconds{") || strings.Contains(out, "vidra_job_stale_running{") {
		t.Errorf("no job-health samples expected on source error\n%s", out)
	}
}

func TestIPFSCapacityGauges(t *testing.T) {
	used, free := int64(900), int64(50)
	m := NewMetrics()
	m.RegisterIPFSCapacitySource(func(context.Context) (IPFSCapacity, error) {
		return IPFSCapacity{BudgetBytes: 1000, ReservedBytes: 100, MinFreeBytes: 40, RepoUsedBytes: &used, FilesystemFreeBytes: &free, PausedReason: "budget_exhausted"}, nil
	})
	out := scrape(t, m)
	for _, want := range []string{
		"vidra_ipfs_budget_bytes 1000", "vidra_ipfs_reserved_bytes 100", "vidra_ipfs_min_free_bytes 40",
		"vidra_ipfs_repo_used_bytes 900", "vidra_ipfs_filesystem_free_bytes 50",
		`vidra_ipfs_admission_paused{reason="budget_exhausted"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n%s", want, out)
		}
	}
}

func TestIPFSCapacityGaugesOmitUnknownAndResume(t *testing.T) {
	m := NewMetrics()
	m.RegisterIPFSCapacitySource(func(context.Context) (IPFSCapacity, error) {
		return IPFSCapacity{BudgetBytes: 1000}, nil // admitting, host has not reported
	})
	out := scrape(t, m)
	if strings.Contains(out, "vidra_ipfs_repo_used_bytes ") || strings.Contains(out, "vidra_ipfs_filesystem_free_bytes ") || strings.Contains(out, "vidra_ipfs_admission_paused{") {
		t.Errorf("unknown values and an admitting mirror must export no sample\n%s", out)
	}
}

func TestIPFSCapacityGaugesSwallowSourceError(t *testing.T) {
	m := NewMetrics()
	m.RegisterIPFSCapacitySource(func(context.Context) (IPFSCapacity, error) { return IPFSCapacity{}, errors.New("db down") })
	if out := scrape(t, m); strings.Contains(out, "vidra_ipfs_budget_bytes ") {
		t.Errorf("no samples expected on source error\n%s", out)
	}
}
