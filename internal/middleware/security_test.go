package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecurityHeaders(t *testing.T) {
	handler := SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name         string
		proto        string
		tls          bool
		expectedHSTS bool
	}{
		{
			name:         "HTTP request - no HSTS",
			proto:        "http",
			tls:          false,
			expectedHSTS: false,
		},
		{
			name:         "HTTPS request - includes HSTS",
			proto:        "https",
			tls:          false,
			expectedHSTS: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.proto == "https" {
				req.Header.Set("X-Forwarded-Proto", "https")
			}

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// Check security headers
			assert.Equal(t, "DENY", rr.Header().Get("X-Frame-Options"))
			assert.Equal(t, "nosniff", rr.Header().Get("X-Content-Type-Options"))
			assert.Equal(t, "1; mode=block", rr.Header().Get("X-XSS-Protection"))
			assert.Equal(t, "strict-origin-when-cross-origin", rr.Header().Get("Referrer-Policy"))
			assert.Contains(t, rr.Header().Get("Permissions-Policy"), "camera=()")

			csp := rr.Header().Get("Content-Security-Policy")
			assert.Contains(t, csp, "default-src 'self'")
			assert.Contains(t, csp, "frame-ancestors 'none'")
			assert.Contains(t, csp, "upgrade-insecure-requests")

			if tt.expectedHSTS {
				hsts := rr.Header().Get("Strict-Transport-Security")
				assert.Contains(t, hsts, "max-age=63072000")
				assert.Contains(t, hsts, "includeSubDomains")
				assert.Contains(t, hsts, "preload")
			} else {
				assert.Empty(t, rr.Header().Get("Strict-Transport-Security"))
			}
		})
	}
}

func TestRequestID(t *testing.T) {
	handler := RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("generates request ID when missing", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		requestID := rr.Header().Get("X-Request-ID")
		assert.NotEmpty(t, requestID)
		assert.Greater(t, len(requestID), 10)
	})

	t.Run("preserves existing request ID", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		existingID := "test-request-id-123"
		req.Header.Set("X-Request-ID", existingID)

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		assert.Equal(t, existingID, rr.Header().Get("X-Request-ID"))
	})
}

func TestSizeLimiter(t *testing.T) {
	maxSize := int64(1024) // 1KB limit

	// Helper to create handler with optional override function
	createHandler := func(limitFunc func(r *http.Request) int64) http.Handler {
		return SizeLimiter(maxSize, limitFunc)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Try to read more than any expected limit to trigger error
			// Max test case uses 6KB body, limit is 5KB
			body := make([]byte, 10*1024)
			_, err := r.Body.Read(body)
			// MaxBytesReader returns error when limit is exceeded, but might read up to limit first
			if err != nil && err.Error() == "http: request body too large" {
				http.Error(w, "Request too large", http.StatusRequestEntityTooLarge)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
	}

	t.Run("allows requests within size limit", func(t *testing.T) {
		handler := createHandler(nil)
		body := strings.NewReader(strings.Repeat("a", 512))
		req := httptest.NewRequest(http.MethodPost, "/", body)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("rejects requests exceeding size limit", func(t *testing.T) {
		handler := createHandler(nil)
		body := strings.NewReader(strings.Repeat("a", 2048))
		req := httptest.NewRequest(http.MethodPost, "/", body)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
	})

	t.Run("respects dynamic override limit", func(t *testing.T) {
		// Override to allow 5KB for /upload path
		limitFunc := func(r *http.Request) int64 {
			if r.URL.Path == "/upload" {
				return 5 * 1024
			}
			return 0
		}
		handler := createHandler(limitFunc)

		// Case 1: Exceeds default limit but within override -> Should Pass
		body := strings.NewReader(strings.Repeat("a", 2048)) // 2KB > 1KB default
		req := httptest.NewRequest(http.MethodPost, "/upload", body)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)

		// Case 2: Exceeds override limit -> Should Fail
		body2 := strings.NewReader(strings.Repeat("a", 6*1024)) // 6KB > 5KB override
		req2 := httptest.NewRequest(http.MethodPost, "/upload", body2)
		rr2 := httptest.NewRecorder()
		handler.ServeHTTP(rr2, req2)
		assert.Equal(t, http.StatusRequestEntityTooLarge, rr2.Code)

		// Case 3: Other path uses default limit -> Should Fail
		body3 := strings.NewReader(strings.Repeat("a", 2048)) // 2KB > 1KB default
		req3 := httptest.NewRequest(http.MethodPost, "/other", body3)
		rr3 := httptest.NewRecorder()
		handler.ServeHTTP(rr3, req3)
		assert.Equal(t, http.StatusRequestEntityTooLarge, rr3.Code)
	})
}

func TestAPIKeyAuth(t *testing.T) {
	validKey := "valid-api-key-123"
	expectedUserID := "user-123"

	validateKey := func(key string) (string, error) {
		if key == validKey {
			return expectedUserID, nil
		}
		return "", assert.AnError
	}

	handler := APIKeyAuth(validateKey)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := r.Context().Value(UserIDKey)
		require.NotNil(t, userID)
		assert.Equal(t, expectedUserID, userID)
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("accepts valid API key in header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-API-Key", validKey)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("rejects API key in query params (security improvement)", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?api_key="+validKey, nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		// API keys in query params are no longer accepted for security reasons
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})

	t.Run("rejects invalid API key", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-API-Key", "invalid-key")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})

	t.Run("rejects missing API key", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})
}
