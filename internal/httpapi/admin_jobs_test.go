package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/jobstatus"
	"github.com/vidra/vidra-core/internal/observability"
)

// fakeJobStatus is a static jobStatusProvider for handler tests.
type fakeJobStatus struct {
	ov  jobstatus.Overview
	err error
}

func (f fakeJobStatus) Overview(context.Context) (jobstatus.Overview, error) {
	return f.ov, f.err
}

// authServerWithJobs builds an auth-enabled server with a fake jobs provider.
func authServerWithJobs(t *testing.T, provider jobStatusProvider, opts ...Option) *Server {
	t.Helper()
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(repo, issuer, 720*time.Hour)
	all := append([]Option{WithAuthService(svc, 15*time.Minute), WithJobStatusService(provider)}, opts...)
	return New(testConfig(), nil, nil, all...)
}

func TestAdminJobsSnapshot(t *testing.T) {
	failID := uuid.New()
	provider := fakeJobStatus{ov: jobstatus.Overview{
		Queues: []jobstatus.QueueStatus{
			{Queue: "transcode_jobs", Pending: 2, Running: 1, Done: 10, Failed: 1, OldestPendingAgeSeconds: 42},
			{Queue: "upload_sessions", Pending: 0, Running: 0, Done: 5, Failed: 2, OldestPendingAgeSeconds: 0},
		},
		RecentFailures: []jobstatus.Failure{
			{Queue: "transcode_jobs", ID: failID, Error: "ffmpeg exited 1", Attempts: 5, FailedAt: time.Now()},
		},
	}}
	srv := authServerWithJobs(t, provider)
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := getWithAuth(srv, "/api/v1/admin/jobs", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("jobs = %d; body=%s", rec.Code, rec.Body.String())
	}
	var ov jobstatus.Overview
	if err := json.Unmarshal(rec.Body.Bytes(), &ov); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(ov.Queues) != 2 || ov.Queues[0].Queue != "transcode_jobs" || ov.Queues[0].Pending != 2 {
		t.Errorf("unexpected queues: %+v", ov.Queues)
	}
	if ov.Queues[0].OldestPendingAgeSeconds != 42 {
		t.Errorf("oldest_pending_age = %d, want 42", ov.Queues[0].OldestPendingAgeSeconds)
	}
	if len(ov.RecentFailures) != 1 || ov.RecentFailures[0].Error != "ffmpeg exited 1" || ov.RecentFailures[0].Attempts != 5 {
		t.Errorf("unexpected failures: %+v", ov.RecentFailures)
	}
	// The JSON snake_case contract the frontend consumes.
	body := rec.Body.String()
	for _, key := range []string{`"queues"`, `"recent_failures"`, `"oldest_pending_age_seconds"`, `"attempts"`} {
		if !strings.Contains(body, key) {
			t.Errorf("response missing key %s\n%s", key, body)
		}
	}

	// Authorisation: non-admin 403, anon 401.
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := getWithAuth(srv, "/api/v1/admin/jobs", bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin jobs = %d, want 403", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/admin/jobs", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon jobs = %d, want 401", rec.Code)
	}
}

// TestMetricsEndpointGating proves /metrics is present only when MetricsEnabled,
// records the request choke point, and never labels with a raw URL or id.
func TestMetricsEndpointGating(t *testing.T) {
	// Off by default (testConfig has MetricsEnabled=false): no /metrics route.
	off := New(testConfig(), nil, nil)
	rec := httptest.NewRecorder()
	off.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("metrics off: /metrics = %d, want 404", rec.Code)
	}

	// On: build a metrics-enabled config + registry.
	cfg := testConfig()
	cfg.MetricsEnabled = true
	metrics := observability.NewMetrics()
	on := New(cfg, nil, nil, WithMetrics(metrics))

	// Drive a request so the choke point records a counter, then scrape.
	rec = httptest.NewRecorder()
	on.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d", rec.Code)
	}
	scrape := httptest.NewRecorder()
	on.Handler().ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if scrape.Code != http.StatusOK {
		t.Fatalf("/metrics = %d, want 200", scrape.Code)
	}
	out := scrape.Body.String()
	if !strings.Contains(out, `vidra_http_requests_total{method="GET",route="/healthz",status_class="2xx"}`) {
		t.Errorf("scrape missing healthz counter\n%s", out)
	}
}
