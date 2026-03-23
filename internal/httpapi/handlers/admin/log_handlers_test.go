package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Mock repository
// ---------------------------------------------------------------------------

type mockLogRepo struct {
	logs      []map[string]interface{}
	auditLogs []*AuditLog
	total     int64
	err       error
}

func (m *mockLogRepo) GetRecentLogs(_ context.Context, _ int) ([]map[string]interface{}, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.logs, nil
}

func (m *mockLogRepo) GetAuditLogs(_ context.Context, _, _ int) ([]*AuditLog, int64, error) {
	if m.err != nil {
		return nil, 0, m.err
	}
	return m.auditLogs, m.total, nil
}

func (m *mockLogRepo) CreateClientLog(_ context.Context, _, _, _, _ string) error {
	return m.err
}

// ---------------------------------------------------------------------------
// GetServerLogs tests
// ---------------------------------------------------------------------------

func TestGetServerLogs_OK(t *testing.T) {
	repo := &mockLogRepo{
		logs: []map[string]interface{}{
			{"level": "info", "message": "server started"},
		},
	}
	h := NewLogHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/logs", nil)
	req = withRole(req, "admin")
	rr := httptest.NewRecorder()

	h.GetServerLogs(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestGetServerLogs_Empty(t *testing.T) {
	repo := &mockLogRepo{logs: nil}
	h := NewLogHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/logs", nil)
	req = withRole(req, "admin")
	rr := httptest.NewRecorder()

	h.GetServerLogs(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp struct {
		Data []map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Empty(t, resp.Data)
}

func TestGetServerLogs_Forbidden(t *testing.T) {
	h := NewLogHandlers(&mockLogRepo{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/logs", nil)
	req = withRole(req, "user")
	rr := httptest.NewRecorder()

	h.GetServerLogs(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

// ---------------------------------------------------------------------------
// GetAuditLogs tests
// ---------------------------------------------------------------------------

func TestGetAuditLogs_OK(t *testing.T) {
	repo := &mockLogRepo{
		auditLogs: []*AuditLog{
			{ID: 1, Action: "user.create", UserID: "admin-1"},
		},
		total: 1,
	}
	h := NewLogHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/audit-logs", nil)
	req = withRole(req, "admin")
	rr := httptest.NewRecorder()

	h.GetAuditLogs(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp struct {
		Data []*AuditLog `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Len(t, resp.Data, 1)
	assert.Equal(t, int64(1), resp.Meta.Total)
}

func TestGetAuditLogs_Forbidden(t *testing.T) {
	h := NewLogHandlers(&mockLogRepo{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/audit-logs", nil)
	req = withRole(req, "moderator")
	rr := httptest.NewRecorder()

	h.GetAuditLogs(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

// ---------------------------------------------------------------------------
// CreateClientLog tests
// ---------------------------------------------------------------------------

func TestCreateClientLog_OK(t *testing.T) {
	repo := &mockLogRepo{}
	h := NewLogHandlers(repo)

	body := `{"level":"error","message":"JavaScript error on page load"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/logs/client", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.CreateClientLog(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestCreateClientLog_DefaultLevel(t *testing.T) {
	repo := &mockLogRepo{}
	h := NewLogHandlers(repo)

	body := `{"message":"Some log message"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/logs/client", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.CreateClientLog(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestCreateClientLog_MissingMessage(t *testing.T) {
	h := NewLogHandlers(&mockLogRepo{})

	body := `{"level":"info"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/logs/client", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.CreateClientLog(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestCreateClientLog_InvalidLevel(t *testing.T) {
	h := NewLogHandlers(&mockLogRepo{})

	body := `{"level":"critical","message":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/logs/client", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.CreateClientLog(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestCreateClientLog_InvalidBody(t *testing.T) {
	h := NewLogHandlers(&mockLogRepo{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/logs/client", strings.NewReader("{bad"))
	rr := httptest.NewRecorder()

	h.CreateClientLog(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}
