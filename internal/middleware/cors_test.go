package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORS(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		expectedStatus int
		checkHeaders   bool
	}{
		{
			name:           "OPTIONS request",
			method:         http.MethodOptions,
			expectedStatus: http.StatusOK,
			checkHeaders:   true,
		},
		{
			name:           "GET request",
			method:         http.MethodGet,
			expectedStatus: http.StatusOK,
			checkHeaders:   true,
		},
		{
			name:           "POST request",
			method:         http.MethodPost,
			expectedStatus: http.StatusOK,
			checkHeaders:   true,
		},
		{
			name:           "PUT request",
			method:         http.MethodPut,
			expectedStatus: http.StatusOK,
			checkHeaders:   true,
		},
		{
			name:           "DELETE request",
			method:         http.MethodDelete,
			expectedStatus: http.StatusOK,
			checkHeaders:   true,
		},
		{
			name:           "PATCH request",
			method:         http.MethodPatch,
			expectedStatus: http.StatusOK,
			checkHeaders:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/test", nil)
			w := httptest.NewRecorder()

			handlerCalled := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handlerCalled = true
				w.WriteHeader(http.StatusOK)
			})

			corsMiddleware := CORS()
			handler := corsMiddleware(next)
			handler.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if tt.checkHeaders {
				expectedHeaders := map[string]string{
					"Access-Control-Allow-Origin":      "*",
					"Access-Control-Allow-Methods":     "GET, POST, PUT, DELETE, OPTIONS, PATCH",
					"Access-Control-Allow-Headers":     "Accept, Authorization, Content-Type, X-CSRF-Token, X-Requested-With, Idempotency-Key",
					"Access-Control-Expose-Headers":    "Link",
					"Access-Control-Allow-Credentials": "true",
					"Access-Control-Max-Age":           "300",
				}

				for header, expectedValue := range expectedHeaders {
					actualValue := w.Header().Get(header)
					if actualValue != expectedValue {
						t.Errorf("Expected header %s to be %s, got %s", header, expectedValue, actualValue)
					}
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

func TestCORSPreflightRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/api/test", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Content-Type, Authorization")

	w := httptest.NewRecorder()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Next handler should not be called for OPTIONS request")
	})

	corsMiddleware := CORS()
	handler := corsMiddleware(next)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	// Verify CORS headers are present
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("Expected Access-Control-Allow-Origin header to be set")
	}

	if w.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("Expected Access-Control-Allow-Methods header to be set")
	}
}

func TestCORSWithActualRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusCreated)
	})

	corsMiddleware := CORS()
	handler := corsMiddleware(next)
	handler.ServeHTTP(w, req)

	if !handlerCalled {
		t.Error("Expected next handler to be called for actual request")
	}

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status %d, got %d", http.StatusCreated, w.Code)
	}

	// Verify CORS headers are still present
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("Expected Access-Control-Allow-Origin header to be set")
	}
}
