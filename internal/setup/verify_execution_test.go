package setup

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestProgramExecution exercises the full server handler through the chi router
// to verify all routes work end-to-end as the real server would serve them.
func TestProgramExecution(t *testing.T) {
	server := NewServer("8080")
	handler := server.Handler()

	t.Run("database page shows individual PostgreSQL fields", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/setup/database", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Fatalf("Database GET returned %d", w.Code)
		}
		body := w.Body.String()
		for _, field := range []string{"POSTGRES_HOST", "POSTGRES_PORT", "POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB", "POSTGRES_SSLMODE"} {
			if !strings.Contains(body, field) {
				t.Errorf("Database page missing field: %s", field)
			}
		}
	})

	t.Run("database POST with external mode redirects to services", func(t *testing.T) {
		form := "POSTGRES_MODE=external&POSTGRES_HOST=myhost&POSTGRES_PORT=5432&POSTGRES_USER=myuser&POSTGRES_PASSWORD=mypass&POSTGRES_DB=mydb&POSTGRES_SSLMODE=disable"
		req := httptest.NewRequest("POST", "/setup/database", strings.NewReader(form))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != 303 {
			t.Fatalf("Database POST returned %d (expected 303)", w.Code)
		}
		if loc := w.Header().Get("Location"); loc != "/setup/services" {
			t.Fatalf("Database POST redirected to %s (expected /setup/services)", loc)
		}
	})

	t.Run("services page shows test connection buttons", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/setup/services", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Fatalf("Services GET returned %d", w.Code)
		}
		body := w.Body.String()
		for _, fn := range []string{"testRedis()", "testIPFS()", "testIOTA()"} {
			if !strings.Contains(body, fn) {
				t.Errorf("Services page missing test function: %s", fn)
			}
		}
	})

	// Each test connection endpoint uses a different IP to avoid shared rate limiter
	t.Run("test-database endpoint returns JSON error for missing fields", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/setup/test-database", strings.NewReader(`{"host":"","port":0,"user":"","password":""}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.0.0.1:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Fatalf("test-database returned %d", w.Code)
		}
		if !strings.Contains(w.Body.String(), `"success":false`) {
			t.Fatal("test-database didn't return JSON error response")
		}
	})

	t.Run("test-redis endpoint returns JSON error for missing fields", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/setup/test-redis", strings.NewReader(`{"host":"","port":0}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.0.0.2:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Fatalf("test-redis returned %d", w.Code)
		}
		if !strings.Contains(w.Body.String(), `"success":false`) {
			t.Fatal("test-redis didn't return JSON error response")
		}
	})

	t.Run("test-ipfs endpoint returns JSON error for empty URL", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/setup/test-ipfs", strings.NewReader(`{"url":""}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.0.0.3:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Fatalf("test-ipfs returned %d", w.Code)
		}
		if !strings.Contains(w.Body.String(), `"success":false`) {
			t.Fatal("test-ipfs didn't return JSON error response")
		}
	})

	t.Run("test-iota endpoint returns JSON error for empty URL", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/setup/test-iota", strings.NewReader(`{"url":""}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.0.0.4:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Fatalf("test-iota returned %d", w.Code)
		}
		if !strings.Contains(w.Body.String(), `"success":false`) {
			t.Fatal("test-iota didn't return JSON error response")
		}
	})

	t.Run("review page masks password", func(t *testing.T) {
		// First, configure external PostgreSQL
		form := "POSTGRES_MODE=external&POSTGRES_HOST=host&POSTGRES_PORT=5432&POSTGRES_USER=user&POSTGRES_PASSWORD=secret123&POSTGRES_DB=db&POSTGRES_SSLMODE=disable"
		req := httptest.NewRequest("POST", "/setup/database", strings.NewReader(form))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		// Now render review page
		req = httptest.NewRequest("GET", "/setup/review", nil)
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		body := w.Body.String()
		if strings.Contains(body, "secret123") {
			t.Fatal("Review page exposes password in plain text!")
		}
		if !strings.Contains(body, fmt.Sprintf("%c%c%c%c", 0x2022, 0x2022, 0x2022, 0x2022)) {
			t.Fatal("Review page missing password mask (bullet points)")
		}
	})

	t.Run("quick install page shows admin and domain fields", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/setup/quickinstall", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Fatalf("Quick Install GET returned %d", w.Code)
		}
		body := w.Body.String()
		for _, field := range []string{"ADMIN_USERNAME", "ADMIN_EMAIL", "ADMIN_PASSWORD", "ADMIN_PASSWORD_CONFIRM", "NGINX_DOMAIN"} {
			if !strings.Contains(body, field) {
				t.Errorf("Quick Install page missing field: %s", field)
			}
		}
	})
}
