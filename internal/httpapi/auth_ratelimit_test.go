package httpapi

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/ratelimit"
)

func newAuthLimitedServer(t *testing.T, buf *bytes.Buffer, limit int, fc *fakeCounter) *Server {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(repo, issuer, 720*time.Hour)
	authLimiter := ratelimit.NewLimiter(fc, limit, time.Minute)
	return New(testConfig(), nil, nil,
		WithAuthService(svc, 15*time.Minute),
		WithAuthRateLimiter(authLimiter),
		WithLogger(logger),
	)
}

func TestAuthRateLimitThrottlesLoginAndAudits(t *testing.T) {
	var buf bytes.Buffer
	srv := newAuthLimitedServer(t, &buf, 2, &fakeCounter{})
	body := `{"email":"ada@example.test","password":"wrong"}`

	// The first two attempts are within budget (they 401 on bad creds, not 429).
	for i := 1; i <= 2; i++ {
		if rec := postTo(srv, "/api/v1/auth/login", body); rec.Code == http.StatusTooManyRequests {
			t.Fatalf("attempt #%d throttled too early (status 429)", i)
		}
	}
	// The third attempt is denied by the stricter auth limiter.
	rec := postTo(srv, "/api/v1/auth/login", body)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt #3 status = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 response missing Retry-After header")
	}

	// Denial is audited (and carries no credentials).
	events := auditEvents(t, &buf)
	if findAudit(events, observability.ActionRateLimited, observability.ResultFailure) == nil {
		t.Error("expected an auth.rate_limited audit event")
	}
	if bytes.Contains(buf.Bytes(), []byte("wrong")) {
		t.Error("the attempted password must never appear in logs")
	}
}

func TestAuthRateLimitFailsOpen(t *testing.T) {
	var buf bytes.Buffer
	srv := newAuthLimitedServer(t, &buf, 1, &fakeCounter{err: errors.New("redis down")})
	// Beyond the limit, a store error must fail open (login still 401, never 429).
	for i := 1; i <= 3; i++ {
		if rec := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"wrong"}`); rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request #%d throttled, want fail-open on store error", i)
		}
	}
}

func TestAuthRateLimitOnlyOnSensitiveRoutes(t *testing.T) {
	var buf bytes.Buffer
	srv := newAuthLimitedServer(t, &buf, 1, &fakeCounter{})
	// /logout is not in the sensitive set, so the stricter auth limiter never
	// throttles it (it stays idempotent 204 regardless of count).
	for i := 1; i <= 3; i++ {
		rec := postTo(srv, "/api/v1/auth/logout", `{"refresh_token":"x"}`)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("logout #%d throttled, but it is not a sensitive auth route", i)
		}
	}
}
