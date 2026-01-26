package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"athena/internal/config"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"

	"github.com/go-chi/chi/v5"
)

// TestSecurityRoutes_RateLimiting verifies that strict rate limits are applied to sensitive endpoints
// regardless of the general API rate limit configuration.
func TestSecurityRoutes_RateLimiting(t *testing.T) {
	// Setup dependencies
	cfg := &config.Config{
		// Set general rate limit high enough so it doesn't interfere with our strict limit tests
		RateLimitRequests: 1000,
		RateLimitDuration: 60 * time.Second,
		JWTSecret:         "test-secret",
		LogLevel:          "error", // Reduce noise
	}

	rlManager := middleware.NewRateLimiterManager()
	deps := &shared.HandlerDependencies{
		// We leave repos as nil because we expect the rate limiter to block
		// requests before they reach the handler logic that would use these repos.
		// For allowed requests, we expect a 400 Bad Request due to invalid JSON/missing fields,
		// which also happens before repo usage.
	}

	r := chi.NewRouter()
	RegisterRoutesWithDependencies(r, cfg, rlManager, deps)

	t.Run("Login Strict Rate Limit", func(t *testing.T) {
		// strictLoginLimiter is configured for 10 requests per minute in routes.go
		limit := 10
		endpoint := "/auth/login"
		// Use a unique IP for this test to avoid interference
		clientIP := "10.0.0.1"

		// Consuming the allowed burst
		for i := 1; i <= limit; i++ {
			req := httptest.NewRequest("POST", endpoint, bytes.NewBufferString("{}"))
			req.Header.Set("X-Real-IP", clientIP)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			// We expect 400 Bad Request (because body is empty/invalid), but NOT 429
			if w.Code == http.StatusTooManyRequests {
				t.Fatalf("Request %d was rate limited prematurely", i)
			}
			if w.Code != http.StatusBadRequest {
				t.Logf("Request %d returned unexpected status: %d", i, w.Code)
			}
		}

		// The next request should be rate limited
		req := httptest.NewRequest("POST", endpoint, bytes.NewBufferString("{}"))
		req.Header.Set("X-Real-IP", clientIP)
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		if w.Code != http.StatusTooManyRequests {
			t.Errorf("Expected 429 Too Many Requests after exceeding limit, got %d", w.Code)
		}
	})

	t.Run("Register Strict Rate Limit", func(t *testing.T) {
		// strictAuthLimiter (registration) is configured for 5 requests per minute in routes.go
		limit := 5
		endpoint := "/auth/register"
		clientIP := "10.0.0.2"

		// Consuming the allowed burst
		for i := 1; i <= limit; i++ {
			req := httptest.NewRequest("POST", endpoint, bytes.NewBufferString("{}"))
			req.Header.Set("X-Real-IP", clientIP)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code == http.StatusTooManyRequests {
				t.Fatalf("Request %d was rate limited prematurely", i)
			}
		}

		// The next request should be rate limited
		req := httptest.NewRequest("POST", endpoint, bytes.NewBufferString("{}"))
		req.Header.Set("X-Real-IP", clientIP)
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		if w.Code != http.StatusTooManyRequests {
			t.Errorf("Expected 429 Too Many Requests after exceeding limit, got %d", w.Code)
		}
	})
}
