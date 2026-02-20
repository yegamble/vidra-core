package setup

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHandleTestDatabaseInvalidJSON(t *testing.T) {
	wizard := NewWizard()

	req := httptest.NewRequest(http.MethodPost, "/setup/test-database", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	wizard.HandleTestDatabase(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleTestDatabaseMissingFields(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		errMsg string
	}{
		{
			name:   "missing host",
			body:   `{"host":"","port":5432,"user":"u","password":"p"}`,
			errMsg: "Host, user, and password are required",
		},
		{
			name:   "missing user",
			body:   `{"host":"h","port":5432,"user":"","password":"p"}`,
			errMsg: "Host, user, and password are required",
		},
		{
			name:   "missing password",
			body:   `{"host":"h","port":5432,"user":"u","password":""}`,
			errMsg: "Host, user, and password are required",
		},
		{
			name:   "invalid port zero",
			body:   `{"host":"h","port":0,"user":"u","password":"p"}`,
			errMsg: "Invalid port",
		},
		{
			name:   "invalid port too high",
			body:   `{"host":"h","port":99999,"user":"u","password":"p"}`,
			errMsg: "Invalid port",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wizard := NewWizard()
			req := httptest.NewRequest(http.MethodPost, "/setup/test-database", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			wizard.HandleTestDatabase(w, req)

			assert.Equal(t, http.StatusOK, w.Code) // Returns 200 with JSON error
			assert.Contains(t, w.Body.String(), tt.errMsg)
			assert.Contains(t, w.Body.String(), `"success":false`)
		})
	}
}

func TestHandleTestRedisInvalidJSON(t *testing.T) {
	wizard := NewWizard()

	req := httptest.NewRequest(http.MethodPost, "/setup/test-redis", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	wizard.HandleTestRedis(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleTestRedisMissingFields(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		errMsg string
	}{
		{
			name:   "missing host",
			body:   `{"host":"","port":6379}`,
			errMsg: "Host is required",
		},
		{
			name:   "invalid port",
			body:   `{"host":"h","port":0}`,
			errMsg: "Invalid port",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wizard := NewWizard()
			req := httptest.NewRequest(http.MethodPost, "/setup/test-redis", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			wizard.HandleTestRedis(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), tt.errMsg)
			assert.Contains(t, w.Body.String(), `"success":false`)
		})
	}
}

func TestHandleTestIPFSInvalidJSON(t *testing.T) {
	wizard := NewWizard()

	req := httptest.NewRequest(http.MethodPost, "/setup/test-ipfs", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	wizard.HandleTestIPFS(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleTestIPFSEmptyURL(t *testing.T) {
	wizard := NewWizard()

	req := httptest.NewRequest(http.MethodPost, "/setup/test-ipfs", strings.NewReader(`{"url":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	wizard.HandleTestIPFS(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "URL is required")
	assert.Contains(t, w.Body.String(), `"success":false`)
}

func TestHandleTestIOTAInvalidJSON(t *testing.T) {
	wizard := NewWizard()

	req := httptest.NewRequest(http.MethodPost, "/setup/test-iota", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	wizard.HandleTestIOTA(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleTestIOTAEmptyURL(t *testing.T) {
	wizard := NewWizard()

	req := httptest.NewRequest(http.MethodPost, "/setup/test-iota", strings.NewReader(`{"url":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	wizard.HandleTestIOTA(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "URL is required")
	assert.Contains(t, w.Body.String(), `"success":false`)
}

func TestHandleTestConnectionRateLimiting(t *testing.T) {
	wizard := NewWizard()

	// Send 4 requests - 4th should be rate limited
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest(http.MethodPost, "/setup/test-ipfs", strings.NewReader(`{"url":""}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.168.1.100:12345"
		w := httptest.NewRecorder()
		wizard.HandleTestIPFS(w, req)

		if i < 3 {
			// First 3 requests should succeed (with validation error, not rate limit)
			assert.Equal(t, http.StatusOK, w.Code, "request %d should not be rate limited", i+1)
		} else {
			// 4th request should be rate limited
			assert.Equal(t, http.StatusTooManyRequests, w.Code, "request %d should be rate limited", i+1)
		}
	}
}

func TestRespondTestConnectionHelpers(t *testing.T) {
	t.Run("success response", func(t *testing.T) {
		w := httptest.NewRecorder()
		respondTestConnectionSuccess(w, "Connection OK")

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"success":true`)
		assert.Contains(t, w.Body.String(), "Connection OK")
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	})

	t.Run("error response", func(t *testing.T) {
		w := httptest.NewRecorder()
		respondTestConnectionError(w, "Connection failed")

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"success":false`)
		assert.Contains(t, w.Body.String(), "Connection failed")
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	})
}
