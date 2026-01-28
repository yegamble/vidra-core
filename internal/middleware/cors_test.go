package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORS(t *testing.T) {
	tests := []struct {
		name           string
		allowedOrigins string
		requestOrigin  string
		method         string
		expectedStatus int
		expectHeaders  bool
		expectedOrigin string
	}{
		{
			name:           "OPTIONS request with match",
			allowedOrigins: "https://example.com",
			requestOrigin:  "https://example.com",
			method:         http.MethodOptions,
			expectedStatus: http.StatusOK,
			expectHeaders:  true,
			expectedOrigin: "https://example.com",
		},
		{
			name:           "GET request with match",
			allowedOrigins: "https://example.com",
			requestOrigin:  "https://example.com",
			method:         http.MethodGet,
			expectedStatus: http.StatusOK,
			expectHeaders:  true,
			expectedOrigin: "https://example.com",
		},
		{
			name:           "GET request with mismatch",
			allowedOrigins: "https://example.com",
			requestOrigin:  "https://attacker.com",
			method:         http.MethodGet,
			expectedStatus: http.StatusOK,
			expectHeaders:  false,
		},
		{
			name:           "Wildcard allowed",
			allowedOrigins: "*",
			requestOrigin:  "https://foo.com",
			method:         http.MethodGet,
			expectedStatus: http.StatusOK,
			expectHeaders:  true,
			expectedOrigin: "https://foo.com",
		},
		{
			name:           "No Origin header",
			allowedOrigins: "*",
			requestOrigin:  "",
			method:         http.MethodGet,
			expectedStatus: http.StatusOK,
			expectHeaders:  false,
		},
		{
			name:           "Multiple allowed origins",
			allowedOrigins: "https://a.com, https://b.com",
			requestOrigin:  "https://b.com",
			method:         http.MethodGet,
			expectedStatus: http.StatusOK,
			expectHeaders:  true,
			expectedOrigin: "https://b.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/test", nil)
			if tt.requestOrigin != "" {
				req.Header.Set("Origin", tt.requestOrigin)
			}

			w := httptest.NewRecorder()

			handlerCalled := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handlerCalled = true
				w.WriteHeader(http.StatusOK)
			})

			corsMiddleware := CORS(tt.allowedOrigins)
			handler := corsMiddleware(next)
			handler.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if tt.expectHeaders {
				if val := w.Header().Get("Access-Control-Allow-Origin"); val != tt.expectedOrigin {
					t.Errorf("Expected Access-Control-Allow-Origin %s, got %s", tt.expectedOrigin, val)
				}
				if val := w.Header().Get("Access-Control-Allow-Credentials"); val != "true" {
					t.Errorf("Expected Access-Control-Allow-Credentials true, got %s", val)
				}
				if val := w.Header().Get("Vary"); val != "Origin" {
					t.Errorf("Expected Vary Origin, got %s", val)
				}
			} else {
				if val := w.Header().Get("Access-Control-Allow-Origin"); val != "" {
					t.Errorf("Expected no Access-Control-Allow-Origin, got %s", val)
				}
			}

			// For OPTIONS, next handler should not be called
			if tt.method == http.MethodOptions {
				if handlerCalled {
					t.Error("Expected next handler not to be called for OPTIONS request")
				}
			} else {
				if !handlerCalled {
					t.Error("Expected next handler to be called for non-OPTIONS request")
				}
			}
		})
	}
}
