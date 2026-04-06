package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeJSONLLog writes JSONL entries to a temp log file and returns its path.
func writeJSONLLog(t *testing.T, dir, filename string, entries []map[string]interface{}) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()
	for _, e := range entries {
		b, _ := json.Marshal(e)
		f.Write(b)
		f.WriteString("\n")
	}
	return path
}

// newTestHandlers creates a LogHandlers with a temp log dir and test log files.
func newTestHandlers(t *testing.T, logEntries, auditEntries []map[string]interface{}) (*LogHandlers, string) {
	dir := t.TempDir()
	if len(logEntries) > 0 {
		writeJSONLLog(t, dir, "vidra.log", logEntries)
	}
	if len(auditEntries) > 0 {
		writeJSONLLog(t, dir, "vidra-audit.log", auditEntries)
	}
	return NewLogHandlers(dir, "vidra.log", "vidra-audit.log", true), dir
}

// ---------------------------------------------------------------------------
// GetServerLogs tests
// ---------------------------------------------------------------------------

func TestGetServerLogs_Forbidden(t *testing.T) {
	h := NewLogHandlers("", "vidra.log", "vidra-audit.log", true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/logs", nil)
	req = withRole(req, "user")
	rr := httptest.NewRecorder()

	h.GetServerLogs(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestGetServerLogs_MissingStartDate(t *testing.T) {
	h := NewLogHandlers("", "vidra.log", "vidra-audit.log", true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/logs", nil)
	req = withRole(req, "admin")
	rr := httptest.NewRecorder()

	h.GetServerLogs(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestGetServerLogs_EmptyDir(t *testing.T) {
	h := NewLogHandlers("", "vidra.log", "vidra-audit.log", true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/logs?startDate=2026-01-01T00:00:00Z", nil)
	req = withRole(req, "admin")
	rr := httptest.NewRecorder()

	h.GetServerLogs(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestGetServerLogs_WithEntries(t *testing.T) {
	now := time.Now().UTC()
	entries := []map[string]interface{}{
		{"timestamp": now.Format(time.RFC3339Nano), "level": "INFO", "msg": "server started"},
		{"timestamp": now.Add(-time.Hour).Format(time.RFC3339Nano), "level": "WARN", "msg": "high memory"},
	}
	h, _ := newTestHandlers(t, entries, nil)

	startDate := now.Add(-2 * time.Hour).Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/logs?startDate="+startDate, nil)
	req = withRole(req, "admin")
	rr := httptest.NewRecorder()

	h.GetServerLogs(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)

	var resp struct {
		Data []map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Len(t, resp.Data, 2)
}

func TestGetServerLogs_LevelFilter(t *testing.T) {
	now := time.Now().UTC()
	entries := []map[string]interface{}{
		{"timestamp": now.Format(time.RFC3339Nano), "level": "info", "msg": "info message"},
		{"timestamp": now.Add(-time.Minute).Format(time.RFC3339Nano), "level": "warn", "msg": "warn message"},
		{"timestamp": now.Add(-2 * time.Minute).Format(time.RFC3339Nano), "level": "debug", "msg": "debug message"},
	}
	h, _ := newTestHandlers(t, entries, nil)

	startDate := now.Add(-10 * time.Minute).Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/logs?startDate="+startDate+"&level=warn", nil)
	req = withRole(req, "admin")
	rr := httptest.NewRecorder()

	h.GetServerLogs(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)

	var resp struct {
		Data []map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	// Only warn and above (no debug, no info)
	assert.Len(t, resp.Data, 1)
	assert.Equal(t, "warn", resp.Data[0]["level"])
}

func TestGetServerLogs_TagsFilter(t *testing.T) {
	now := time.Now().UTC()
	entries := []map[string]interface{}{
		{"timestamp": now.Format(time.RFC3339Nano), "level": "info", "msg": "http request", "tags": []string{"http"}},
		{"timestamp": now.Add(-time.Minute).Format(time.RFC3339Nano), "level": "info", "msg": "ap event", "tags": []string{"ap", "video"}},
		{"timestamp": now.Add(-2 * time.Minute).Format(time.RFC3339Nano), "level": "info", "msg": "other", "tags": []string{"other"}},
	}
	h, _ := newTestHandlers(t, entries, nil)

	startDate := now.Add(-10 * time.Minute).Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/server/logs?startDate=%s&tagsOneOf=http,ap", startDate), nil)
	req = withRole(req, "admin")
	rr := httptest.NewRecorder()

	h.GetServerLogs(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)

	var resp struct {
		Data []map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	// Should return entries tagged with http or ap (not "other")
	assert.Len(t, resp.Data, 2)
}

// ---------------------------------------------------------------------------
// GetAuditLogs tests
// ---------------------------------------------------------------------------

func TestGetAuditLogs_Forbidden(t *testing.T) {
	h := NewLogHandlers("", "vidra.log", "vidra-audit.log", true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/audit-logs", nil)
	req = withRole(req, "moderator")
	rr := httptest.NewRecorder()

	h.GetAuditLogs(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestGetAuditLogs_WithEntries(t *testing.T) {
	now := time.Now().UTC()
	auditEntries := []map[string]interface{}{
		{"timestamp": now.Format(time.RFC3339Nano), "action": "create", "domain": "videos", "user": "alice"},
		{"timestamp": now.Add(-time.Hour).Format(time.RFC3339Nano), "action": "delete", "domain": "users", "user": "admin"},
	}
	h, _ := newTestHandlers(t, nil, auditEntries)

	startDate := now.Add(-2 * time.Hour).Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/audit-logs?startDate="+startDate, nil)
	req = withRole(req, "admin")
	rr := httptest.NewRecorder()

	h.GetAuditLogs(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)

	var resp struct {
		Data []map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Len(t, resp.Data, 2)
}

// ---------------------------------------------------------------------------
// CreateClientLog tests
// ---------------------------------------------------------------------------

func TestCreateClientLog_OK(t *testing.T) {
	h := NewLogHandlers("", "vidra.log", "vidra-audit.log", true)

	body := `{"level":"error","message":"JavaScript error on page load"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/logs/client", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.CreateClientLog(rr, req)
	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestCreateClientLog_DefaultLevel(t *testing.T) {
	h := NewLogHandlers("", "vidra.log", "vidra-audit.log", true)

	body := `{"message":"Some log message"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/logs/client", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.CreateClientLog(rr, req)
	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestCreateClientLog_MissingMessage(t *testing.T) {
	h := NewLogHandlers("", "vidra.log", "vidra-audit.log", true)

	body := `{"level":"info"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/logs/client", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.CreateClientLog(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestCreateClientLog_InvalidLevel(t *testing.T) {
	h := NewLogHandlers("", "vidra.log", "vidra-audit.log", true)

	body := `{"level":"critical","message":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/logs/client", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.CreateClientLog(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestCreateClientLog_InvalidBody(t *testing.T) {
	h := NewLogHandlers("", "vidra.log", "vidra-audit.log", true)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/logs/client", strings.NewReader("{bad"))
	rr := httptest.NewRecorder()

	h.CreateClientLog(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestCreateClientLog_Disabled(t *testing.T) {
	h := NewLogHandlers("", "vidra.log", "vidra-audit.log", false)

	body := `{"message":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/logs/client", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.CreateClientLog(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}
