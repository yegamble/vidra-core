package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/config"
)

// fakePinger is a test double for a dependency probe.
type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func testConfig() *config.Config {
	return &config.Config{
		Environment: "test",
		// The production default: boot normalises an unset VIDRA_ROLE to "all",
		// so a hand-built test config carries the same shape the server ships.
		Role:                config.RoleAll,
		HTTPHost:            "127.0.0.1",
		HTTPPort:            8080,
		CORSAllowedOrigins:  []string{"http://localhost:3000"},
		InstanceName:        "Vidra Test",
		RegistrationEnabled: true,
		HTTPRequestTimeout:  30 * time.Second,
		// The byte-streaming budget (chunk PUTs, media GETs) mirrors the production
		// default so tests exercise the same two-deadline shape the server ships.
		HTTPStreamRequestTimeout: time.Hour,
		HTTPBodyLimit:            "8M",
		UploadMaxSize:            "64K",
		// Mirror the production batch-upload guard default (UPLOAD-10); well above
		// what any single-user test holds open, so it is inert unless a test lowers
		// it. Tests exercising the guard build a config with a small cap.
		UploadMaxActiveSessionsPerUser: 5,
		JWTRefreshTTL:                  720 * time.Hour,
		// Effective rate-limit config mirrors the production defaults so the admin
		// system-status page (which surfaces these read-only) has real values to
		// report. The httpapi layer only enforces limits when a limiter is
		// injected (WithRateLimiter), so these are inert unless a test wires one.
		RateLimitEnabled:      true,
		RateLimitRequests:     120,
		AuthRateLimitRequests: 10,
		RateLimitWindow:       time.Minute,
		// Feature gates open, so upload/import/live/comment tests work unless a
		// test flips them. Uploads/imports/comments mirror the production
		// default; live's production default is DERIVED (on only when
		// LIVE_RTMP_URL is set) and is pinned on here because the live
		// handler tests predate that and exercise the enabled path.
		UploadsEnabled:  true,
		ImportsEnabled:  true,
		LiveEnabled:     true,
		CommentsEnabled: true,
	}
}

func TestHealthz(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body livenessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
}

func TestReadyzAllHealthy(t *testing.T) {
	srv := New(testConfig(), fakePinger{}, fakePinger{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body readinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	if body.Components["postgres"].Status != "ok" || body.Components["redis"].Status != "ok" {
		t.Errorf("components = %+v, want both ok", body.Components)
	}
}

// getReadyz drives GET /readyz once and returns the code and the decoded body.
func getReadyz(t *testing.T, srv *Server) (int, readinessResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var body readinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return rec.Code, body
}

// Postgres is the one dependency that takes an instance out of rotation: it is
// the system of record, and a replica that cannot reach it has nothing to serve.
func TestReadyzPostgresDownIs503(t *testing.T) {
	srv := New(testConfig(), fakePinger{err: errors.New("connection refused")}, fakePinger{})
	code, body := getReadyz(t, srv)

	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", code)
	}
	if body.Status != "unavailable" {
		t.Errorf("status = %q, want unavailable", body.Status)
	}
	if body.Components["postgres"].Status != "down" {
		t.Errorf("postgres status = %q, want down", body.Components["postgres"].Status)
	}
}

// Redis is NOT. Every rate limiter fails open on a Redis error, so a Redis blip
// is a degradation — and 503ing on it would pull every replica in the fleet out
// of rotation at once, since they all share the same Redis.
func TestReadyzRedisDownStaysInRotation(t *testing.T) {
	srv := New(testConfig(), fakePinger{}, fakePinger{err: errors.New("connection refused")})
	code, body := getReadyz(t, srv)

	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a Redis outage must not take this replica out of rotation", code)
	}
	if body.Status != "degraded" {
		t.Errorf("status = %q, want degraded", body.Status)
	}
	// The body still has to say so: a 200 that hid the outage would be worse
	// than a 503.
	if body.Components["redis"].Status != "down" {
		t.Errorf("redis status = %q, want down", body.Components["redis"].Status)
	}
	if body.Components["postgres"].Status != "ok" {
		t.Errorf("postgres status = %q, want ok", body.Components["postgres"].Status)
	}
}

// Draining is the load balancer's cue to stop routing here, and it is answered
// without touching any dependency.
func TestReadyzDraining(t *testing.T) {
	db, rdb := &countingPinger{}, &countingPinger{}
	srv := New(testConfig(), db, rdb)

	if code, _ := getReadyz(t, srv); code != http.StatusOK {
		t.Fatalf("status before drain = %d, want 200", code)
	}
	if srv.Draining() {
		t.Fatal("Draining() is true before Drain()")
	}

	srv.Drain()
	if !srv.Draining() {
		t.Fatal("Draining() is false after Drain()")
	}
	before := db.calls()
	code, body := getReadyz(t, srv)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status while draining = %d, want 503", code)
	}
	if body.Status != "draining" {
		t.Errorf("status = %q, want draining", body.Status)
	}
	if got := db.calls(); got != before {
		t.Errorf("draining probe pinged postgres %d extra times; it must spend no pooled connection", got-before)
	}
}

// The probe result is cached, so the cost of answering readiness does not scale
// with the number of balancers, orchestrators and uptime checks watching it —
// each uncached probe spends a pooled DB connection.
func TestReadyzCachesTheProbe(t *testing.T) {
	db, rdb := &countingPinger{}, &countingPinger{}
	srv := New(testConfig(), db, rdb)

	for i := 0; i < 5; i++ {
		if code, _ := getReadyz(t, srv); code != http.StatusOK {
			t.Fatalf("call %d: status = %d, want 200", i, code)
		}
	}
	if got := db.calls(); got != 1 {
		t.Errorf("postgres pinged %d times for 5 readiness calls, want 1", got)
	}
	if got := rdb.calls(); got != 1 {
		t.Errorf("redis pinged %d times for 5 readiness calls, want 1", got)
	}

	// Aging the cache out re-probes.
	srv.readinessMu.Lock()
	srv.readinessCached.at = time.Now().Add(-2 * readinessCacheTTL)
	srv.readinessMu.Unlock()
	if code, _ := getReadyz(t, srv); code != http.StatusOK {
		t.Fatal("status after cache expiry != 200")
	}
	if got := db.calls(); got != 2 {
		t.Errorf("postgres pinged %d times after the cache aged out, want 2", got)
	}
}

// countingPinger is a healthy Pinger that records how many times it was asked.
type countingPinger struct {
	mu sync.Mutex
	n  int
}

func (p *countingPinger) Ping(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.n++
	return nil
}

func (p *countingPinger) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n
}

func TestNodeInfo(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/nodeinfo", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body nodeInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Software.Name != "vidra" {
		t.Errorf("software.name = %q, want vidra", body.Software.Name)
	}
	if body.Instance.Name != "Vidra Test" {
		t.Errorf("instance.name = %q, want Vidra Test", body.Instance.Name)
	}
}
