package middleware

import (
	"io"
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

	handler := SizeLimiter(maxSize)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, maxSize+1)
		_, err := r.Body.Read(body)
		if err != nil {
			http.Error(w, "Request too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("allows requests within size limit", func(t *testing.T) {
		body := strings.NewReader(strings.Repeat("a", 512))
		req := httptest.NewRequest(http.MethodPost, "/", body)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("rejects requests exceeding size limit", func(t *testing.T) {
		body := strings.NewReader(strings.Repeat("a", 2048))
		req := httptest.NewRequest(http.MethodPost, "/", body)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
	})
}

func TestSizeLimiterWithOverrides(t *testing.T) {
	defaultLimit := int64(1024)
	uploadLimit := int64(4096)

	handler := SizeLimiterWithOverrides(defaultLimit, []RequestSizeOverride{
		{PathPrefix: "/api/v1/uploads", MaxBytes: uploadLimit},
		{PathPrefix: "/api/v1/videos/", PathSuffix: "/upload", MaxBytes: uploadLimit},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "Request too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("uses default limit for normal route", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/videos", strings.NewReader(strings.Repeat("a", 2048)))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
	})

	t.Run("uses upload override for chunk route", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/uploads/session-1/chunks", strings.NewReader(strings.Repeat("a", 2048)))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("uses upload override for video upload route", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/123/upload", strings.NewReader(strings.Repeat("a", 2048)))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
	})
}

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int64
		wantErr bool
	}{
		{name: "raw bytes", input: "1024", want: 1024},
		{name: "megabytes", input: "10MB", want: 10 * 1000 * 1000},
		{name: "mebibytes", input: "10MiB", want: 10 * 1024 * 1024},
		{name: "with spaces", input: " 5 KB ", want: 5 * 1000},
		{name: "invalid", input: "ten", wantErr: true},
		{name: "zero", input: "0", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseByteSize(tc.input)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
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
