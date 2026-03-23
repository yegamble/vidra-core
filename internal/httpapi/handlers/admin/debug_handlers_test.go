package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// GetDebugInfo tests
// ---------------------------------------------------------------------------

func TestGetDebugInfo_OK(t *testing.T) {
	h := NewDebugHandlers()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/debug", nil)
	req = withRole(req, "admin")
	rr := httptest.NewRecorder()

	h.GetDebugInfo(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp struct {
		Data map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.NotEmpty(t, resp.Data["goVersion"])
	assert.NotEmpty(t, resp.Data["uptime"])
}

func TestGetDebugInfo_Forbidden(t *testing.T) {
	h := NewDebugHandlers()

	tests := []struct {
		name string
		role string
	}{
		{"user role", "user"},
		{"moderator role", "moderator"},
		{"empty role", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/server/debug", nil)
			if tt.role != "" {
				req = withRole(req, tt.role)
			}
			rr := httptest.NewRecorder()

			h.GetDebugInfo(rr, req)

			assert.Equal(t, http.StatusForbidden, rr.Code)
		})
	}
}

// ---------------------------------------------------------------------------
// RunCommand tests
// ---------------------------------------------------------------------------

func TestRunCommand_GC(t *testing.T) {
	h := NewDebugHandlers()

	body := `{"command":"gc"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/debug/run-command", strings.NewReader(body))
	req = withRole(req, "admin")
	rr := httptest.NewRecorder()

	h.RunCommand(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestRunCommand_Goroutines(t *testing.T) {
	h := NewDebugHandlers()

	body := `{"command":"goroutines"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/debug/run-command", strings.NewReader(body))
	req = withRole(req, "admin")
	rr := httptest.NewRecorder()

	h.RunCommand(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp struct {
		Data map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.NotNil(t, resp.Data["goroutines"])
}

func TestRunCommand_Memstats(t *testing.T) {
	h := NewDebugHandlers()

	body := `{"command":"memstats"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/debug/run-command", strings.NewReader(body))
	req = withRole(req, "admin")
	rr := httptest.NewRecorder()

	h.RunCommand(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestRunCommand_UnknownCommand(t *testing.T) {
	h := NewDebugHandlers()

	body := `{"command":"rm -rf /"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/debug/run-command", strings.NewReader(body))
	req = withRole(req, "admin")
	rr := httptest.NewRecorder()

	h.RunCommand(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestRunCommand_EmptyCommand(t *testing.T) {
	h := NewDebugHandlers()

	body := `{"command":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/debug/run-command", strings.NewReader(body))
	req = withRole(req, "admin")
	rr := httptest.NewRecorder()

	h.RunCommand(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestRunCommand_InvalidBody(t *testing.T) {
	h := NewDebugHandlers()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/debug/run-command", strings.NewReader("{bad"))
	req = withRole(req, "admin")
	rr := httptest.NewRecorder()

	h.RunCommand(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestRunCommand_Forbidden(t *testing.T) {
	h := NewDebugHandlers()

	body := `{"command":"gc"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/debug/run-command", strings.NewReader(body))
	req = withRole(req, "user")
	rr := httptest.NewRecorder()

	h.RunCommand(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}
