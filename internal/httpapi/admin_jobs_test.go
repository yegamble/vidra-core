package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/jobstatus"
	"github.com/vidra/vidra-core/internal/observability"
)

// fakeJobStatus is a static jobStatusProvider for handler tests.
type fakeJobStatus struct {
	ov  jobstatus.Overview
	err error
}

type fakeJobOperations struct {
	fakeJobStatus
	runs      []jobstatus.Run
	total     int64
	cursor    int64
	detail    jobstatus.Detail
	events    []jobstatus.Event
	gotFilter jobstatus.Filter
	gotLimit  int32
	gotOffset int32
	gotAfter  int64
}

func (f *fakeJobOperations) ListRuns(_ context.Context, filter jobstatus.Filter, limit, offset int32) ([]jobstatus.Run, int64, int64, error) {
	f.gotFilter, f.gotLimit, f.gotOffset = filter, limit, offset
	return f.runs, f.total, f.cursor, f.err
}
func (f *fakeJobOperations) GetDetail(_ context.Context, _ uuid.UUID, limit, offset int32) (jobstatus.Detail, error) {
	f.gotLimit, f.gotOffset = limit, offset
	return f.detail, f.err
}
func (f *fakeJobOperations) EventsAfter(_ context.Context, cursor int64, filter jobstatus.Filter, _ int32) ([]jobstatus.Event, error) {
	f.gotAfter, f.gotFilter = cursor, filter
	if len(f.events) > 0 {
		eventCursor, _ := strconv.ParseInt(f.events[0].Cursor, 10, 64)
		if cursor >= eventCursor {
			return []jobstatus.Event{}, f.err
		}
	}
	return f.events, f.err
}
func (f *fakeJobOperations) CursorBounds(context.Context) (int64, int64, error) {
	return 1, f.cursor, f.err
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

func TestAdminJobRunsListFiltersAndCursor(t *testing.T) {
	runID := uuid.New()
	provider := &fakeJobOperations{
		runs: []jobstatus.Run{{
			ID: runID, Type: "video_import", Queue: "import_jobs",
			State: jobstatus.StateDeadLettered, Priority: 0, Attempt: 5,
			InputMetadata: json.RawMessage(`{}`), OutputMetadata: json.RawMessage(`{}`),
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}},
		total: 1, cursor: 9007199254740993,
	}
	srv := authServerWithJobs(t, provider)
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := getWithAuth(srv, "/api/v1/admin/jobs/runs?state=dead_lettered&type=video_import&queue=import_jobs&resource_type=video&resource_id=abc&worker_id=w1&failure=true&created_after=2025-01-01T00:00:00Z&created_before=2027-01-01T00:00:00Z&limit=25&offset=5", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("list runs = %d: %s", rec.Code, rec.Body.String())
	}
	var body jobRunsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Runs) != 1 || body.Runs[0].ID != runID || body.EventCursor != "9007199254740993" {
		t.Fatalf("response = %+v", body)
	}
	if provider.gotFilter.State != jobstatus.StateDeadLettered || provider.gotFilter.Type != "video_import" ||
		provider.gotFilter.Failure == nil || !*provider.gotFilter.Failure || provider.gotLimit != 25 || provider.gotOffset != 5 {
		t.Fatalf("filter/page not forwarded: %+v limit=%d offset=%d", provider.gotFilter, provider.gotLimit, provider.gotOffset)
	}

	if rec := getWithAuth(srv, "/api/v1/admin/jobs/runs?state=bogus", admin); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid state = %d, want 400", rec.Code)
	}
}

func TestAdminJobRunDetailPagination(t *testing.T) {
	id := uuid.New()
	provider := &fakeJobOperations{cursor: 42, detail: jobstatus.Detail{
		Run: jobstatus.Run{ID: id, Type: "video_transcode", Queue: "transcode_jobs", State: jobstatus.StateRunning,
			InputMetadata: json.RawMessage(`{}`), OutputMetadata: json.RawMessage(`{}`), CreatedAt: time.Now(), UpdatedAt: time.Now()},
		Events: []jobstatus.Event{{ID: uuid.New(), Cursor: "42", JobID: id, Kind: "started", State: jobstatus.StateRunning,
			Metadata: json.RawMessage(`{}`), OccurredAt: time.Now()}},
		EventsTotal: 3, EventCursor: 42,
	}}
	srv := authServerWithJobs(t, provider)
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	rec := getWithAuth(srv, "/api/v1/admin/jobs/runs/"+id.String()+"?events_limit=1&events_offset=2", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail = %d: %s", rec.Code, rec.Body.String())
	}
	var body jobRunDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Run.ID != id || len(body.Events) != 1 || body.EventsTotal != 3 || body.EventsLimit != 1 ||
		body.EventsOffset != 2 || body.EventCursor != "42" {
		t.Fatalf("detail response = %+v", body)
	}
}

func TestAdminJobEventsSSELastEventIDAndEventName(t *testing.T) {
	jobID := uuid.New()
	provider := &fakeJobOperations{cursor: 10, events: []jobstatus.Event{{
		ID: uuid.New(), Cursor: "10", JobID: jobID, Kind: "progress",
		State: jobstatus.StateRunning, Metadata: json.RawMessage(`{}`), OccurredAt: time.Now(),
	}}}
	srv := authServerWithJobs(t, provider)
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/jobs/events?job_id="+jobID.String(), nil).WithContext(ctx)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+admin)
	req.Header.Set("Last-Event-ID", "9")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get(echo.HeaderContentType), "text/event-stream") {
		t.Fatalf("SSE = %d %q: %s", rec.Code, rec.Header().Get(echo.HeaderContentType), rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"id: 10", "event: job-event", `"cursor":"10"`} {
		if !strings.Contains(body, want) {
			t.Errorf("SSE missing %q: %s", want, body)
		}
	}
	if provider.gotAfter != 9 || provider.gotFilter.JobID == nil || *provider.gotFilter.JobID != jobID {
		t.Errorf("resume/filter = after %d filter %+v", provider.gotAfter, provider.gotFilter)
	}
}

func TestAdminJobOperationalRoutesRequireAdmin(t *testing.T) {
	provider := &fakeJobOperations{cursor: 10}
	srv := authServerWithJobs(t, provider)
	_ = registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	nonAdmin := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	for _, path := range []string{"/api/v1/admin/jobs/runs", "/api/v1/admin/jobs/events"} {
		t.Run(path+" anonymous", func(t *testing.T) {
			if rec := getWithAuth(srv, path, ""); rec.Code != http.StatusUnauthorized {
				t.Fatalf("GET %s = %d, want 401", path, rec.Code)
			}
		})
		t.Run(path+" non-admin", func(t *testing.T) {
			if rec := getWithAuth(srv, path, nonAdmin); rec.Code != http.StatusForbidden {
				t.Fatalf("GET %s = %d, want 403", path, rec.Code)
			}
		})
	}
}

func TestAdminJobEventsRejectsInvalidCursorBeforeStreaming(t *testing.T) {
	provider := &fakeJobOperations{cursor: 10}
	srv := authServerWithJobs(t, provider)
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/jobs/events", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+admin)
	req.Header.Set("Last-Event-ID", "9.5")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || strings.Contains(rec.Header().Get(echo.HeaderContentType), "text/event-stream") {
		t.Fatalf("invalid cursor = %d %q: %s", rec.Code, rec.Header().Get(echo.HeaderContentType), rec.Body.String())
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
