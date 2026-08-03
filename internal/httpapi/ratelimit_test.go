package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/ratelimit"
	"github.com/vidra/vidra-core/internal/video"
)

// fakeCounter is an in-memory Counter for middleware tests.
type fakeCounter struct {
	counts map[string]int64
	err    error
}

func (f *fakeCounter) Incr(_ context.Context, key string, window time.Duration) (int64, time.Duration, error) {
	if f.err != nil {
		return 0, 0, f.err
	}
	if f.counts == nil {
		f.counts = map[string]int64{}
	}
	f.counts[key]++
	return f.counts[key], window, nil
}

func newLimitedServer(t *testing.T, limit int, fc *fakeCounter) *Server {
	t.Helper()
	limiter := ratelimit.NewLimiter(fc, limit, time.Minute)
	return New(testConfig(), nil, nil, WithRateLimiter(limiter))
}

func TestRateLimitAllowsThenDenies(t *testing.T) {
	srv := newLimitedServer(t, 2, &fakeCounter{})

	do := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/nodeinfo", nil)
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	for i := 1; i <= 2; i++ {
		rec := do()
		if rec.Code != http.StatusOK {
			t.Fatalf("request #%d status = %d, want 200", i, rec.Code)
		}
		if rec.Header().Get("X-RateLimit-Limit") != "2" {
			t.Errorf("request #%d X-RateLimit-Limit = %q, want 2", i, rec.Header().Get("X-RateLimit-Limit"))
		}
	}

	rec := do()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("request #3 status = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 response missing Retry-After header")
	}
	var body ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Error.Code != "rate_limited" {
		t.Errorf("code = %q, want rate_limited", body.Error.Code)
	}
}

// TestRateLimitExemptsSystemEndpoints ensures liveness probes are never throttled.
func TestRateLimitExemptsSystemEndpoints(t *testing.T) {
	srv := newLimitedServer(t, 1, &fakeCounter{})
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("healthz request #%d status = %d, want 200 (must be exempt)", i, rec.Code)
		}
	}
}

// TestRateLimitFailsOpen ensures a backing-store error allows the request.
func TestRateLimitFailsOpen(t *testing.T) {
	srv := newLimitedServer(t, 1, &fakeCounter{err: errors.New("redis down")})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/nodeinfo", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (fail open when limiter errors)", rec.Code)
	}
}

// TestMediaGetsUseTheirOwnBudget is the regression test for the browsing
// failure: one home page is a single feed call plus ~20 thumbnails, and HLS
// playback adds segments on top, so charging media GETs to the API budget 429s
// an ordinary viewer inside their first minute. Media must draw on its own,
// separately keyed budget.
func TestMediaGetsUseTheirOwnBudget(t *testing.T) {
	fc := &fakeCounter{}
	// A deliberately tiny API budget (1) next to a larger media budget (5), so a
	// leak in either direction is unmissable. The video id is intentionally not a
	// UUID: the handler rejects it with a clean 404 before touching any
	// dependency, which keeps this a pure middleware test.
	srv := New(testConfig(), nil, nil,
		WithRateLimiter(ratelimit.NewLimiter(fc, 1, time.Minute)),
		WithMediaRateLimiter(ratelimit.NewLimiter(fc, 5, time.Minute)),
		WithVideoService(video.NewService(nil, nil)),
		WithChannelService(channel.NewService(nil)),
	)

	for i := 1; i <= 4; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/not-a-uuid/thumbnail", nil)
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("thumbnail request #%d was throttled by the API budget", i)
		}
		if got := rec.Header().Get("X-RateLimit-Limit"); got != "5" {
			t.Fatalf("thumbnail request #%d X-RateLimit-Limit = %q, want the media budget 5", i, got)
		}
	}

	// The API budget is untouched by that burst: its single request still lands.
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/nodeinfo", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("API request status = %d, want 200 — media reads must not spend the API budget", rec.Code)
	}

	// ...and the media budget is genuinely enforced, not merely absent.
	for i := 5; i <= 6; i++ {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/videos/not-a-uuid/thumbnail", nil))
		if i == 6 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("thumbnail request #%d status = %d, want 429 once the media budget is spent", i, rec.Code)
		}
	}
}

// TestNoLimiterMeansNoRateLimiting confirms the API works without the option.
func TestNoLimiterMeansNoRateLimiting(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	for i := 0; i < 10; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/nodeinfo", nil)
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request #%d status = %d, want 200", i, rec.Code)
		}
	}
}
