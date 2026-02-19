package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORS(t *testing.T) {
	tests := []struct {
		name              string
		allowedOrigins    string
		requestOrigin     string
		method            string
		expectedStatus    int
		expectHeaders     bool
		expectedOrigin    string
		expectCredentials bool
	}{
		{
			name:              "allowed origin matches",
			allowedOrigins:    "https://example.com",
			requestOrigin:     "https://example.com",
			method:            http.MethodGet,
			expectedStatus:    http.StatusOK,
			expectHeaders:     true,
			expectedOrigin:    "https://example.com",
			expectCredentials: true,
		},
		{
			name:           "disallowed origin gets no CORS headers",
			allowedOrigins: "https://example.com",
			requestOrigin:  "https://evil.com",
			method:         http.MethodGet,
			expectedStatus: http.StatusOK,
			expectHeaders:  false,
		},
		{
			name:              "wildcard returns * and no credentials",
			allowedOrigins:    "*",
			requestOrigin:     "https://anything.com",
			method:            http.MethodGet,
			expectedStatus:    http.StatusOK,
			expectHeaders:     true,
			expectedOrigin:    "*",
			expectCredentials: false,
		},
		{
			name:           "no origin header means no CORS headers",
			allowedOrigins: "*",
			requestOrigin:  "",
			method:         http.MethodGet,
			expectedStatus: http.StatusOK,
			expectHeaders:  false,
		},
		{
			name:              "multiple allowed origins first match",
			allowedOrigins:    "https://a.com, https://b.com",
			requestOrigin:     "https://a.com",
			method:            http.MethodGet,
			expectedStatus:    http.StatusOK,
			expectHeaders:     true,
			expectedOrigin:    "https://a.com",
			expectCredentials: true,
		},
		{
			name:              "multiple allowed origins second match",
			allowedOrigins:    "https://a.com, https://b.com",
			requestOrigin:     "https://b.com",
			method:            http.MethodPost,
			expectedStatus:    http.StatusOK,
			expectHeaders:     true,
			expectedOrigin:    "https://b.com",
			expectCredentials: true,
		},
		{
			name:           "multiple allowed origins no match",
			allowedOrigins: "https://a.com, https://b.com",
			requestOrigin:  "https://c.com",
			method:         http.MethodGet,
			expectedStatus: http.StatusOK,
			expectHeaders:  false,
		},
		{
			name:              "mixed explicit and wildcard - explicit match",
			allowedOrigins:    "https://example.com, *",
			requestOrigin:     "https://example.com",
			method:            http.MethodGet,
			expectedStatus:    http.StatusOK,
			expectHeaders:     true,
			expectedOrigin:    "https://example.com",
			expectCredentials: true,
		},
		{
			name:              "mixed explicit and wildcard - wildcard match",
			allowedOrigins:    "https://example.com, *",
			requestOrigin:     "https://other.com",
			method:            http.MethodGet,
			expectedStatus:    http.StatusOK,
			expectHeaders:     true,
			expectedOrigin:    "*",
			expectCredentials: false,
		},
		{
			name:              "OPTIONS preflight with allowed origin",
			allowedOrigins:    "https://example.com",
			requestOrigin:     "https://example.com",
			method:            http.MethodOptions,
			expectedStatus:    http.StatusOK,
			expectHeaders:     true,
			expectedOrigin:    "https://example.com",
			expectCredentials: true,
		},
		{
			name:           "OPTIONS preflight with disallowed origin",
			allowedOrigins: "https://example.com",
			requestOrigin:  "https://evil.com",
			method:         http.MethodOptions,
			expectedStatus: http.StatusOK,
			expectHeaders:  false,
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

			handler := CORS(tt.allowedOrigins)(next)
			handler.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			actualOrigin := w.Header().Get("Access-Control-Allow-Origin")
			if tt.expectHeaders {
				if actualOrigin != tt.expectedOrigin {
					t.Errorf("expected Access-Control-Allow-Origin %q, got %q", tt.expectedOrigin, actualOrigin)
				}

				creds := w.Header().Get("Access-Control-Allow-Credentials")
				if tt.expectCredentials {
					if creds != "true" {
						t.Error("expected Access-Control-Allow-Credentials to be true")
					}
				} else {
					if creds != "" {
						t.Errorf("expected Access-Control-Allow-Credentials to be empty, got %q", creds)
					}
				}

				if w.Header().Get("Vary") != "Origin" {
					t.Errorf("expected Vary: Origin header, got %q", w.Header().Get("Vary"))
				}
				if w.Header().Get("Access-Control-Allow-Methods") == "" {
					t.Error("expected Access-Control-Allow-Methods to be set")
				}
			} else {
				if actualOrigin != "" {
					t.Errorf("expected no Access-Control-Allow-Origin, got %q", actualOrigin)
				}
			}

			if tt.method == http.MethodOptions {
				if handlerCalled {
					t.Error("next handler should not be called for OPTIONS request")
				}
			} else {
				if !handlerCalled {
					t.Error("next handler should be called for non-OPTIONS request")
				}
			}
		})
	}
}
