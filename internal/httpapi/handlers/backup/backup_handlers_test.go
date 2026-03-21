package backup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
)

// reqWithID injects a chi URL param "id" into the request context.
func reqWithID(method, path, id string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestListBackupsHandler(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Run("requires authentication", func(t *testing.T) {
		t.Skip("Authentication integration not yet implemented")
	})
}

func TestTriggerBackupHandler(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Run("triggers backup job", func(t *testing.T) {
		t.Skip("Backup integration not yet implemented")
	})
}

func TestDeleteBackupHandler(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Run("deletes backup", func(t *testing.T) {
		t.Skip("Delete integration not yet implemented")
	})
}

func TestRestoreBackupHandler(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Run("starts restore process", func(t *testing.T) {
		t.Skip("Restore integration not yet implemented")
	})
}

func TestGetRestoreStatus_ReturnsOK(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/admin/backups/restore/status", nil)
	w := httptest.NewRecorder()
	h.GetRestoreStatus(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "no active restore")
}

func TestTriggerBackup_InvalidJSON(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/admin/backups", strings.NewReader(`{invalid json`))
	req.ContentLength = int64(len(`{invalid json`))
	w := httptest.NewRecorder()
	h.TriggerBackup(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTriggerBackup_UnsafeExcludeDir(t *testing.T) {
	h := &Handler{}
	body := `{"exclude_dirs":["../../etc"]}`
	req := httptest.NewRequest(http.MethodPost, "/admin/backups", strings.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	h.TriggerBackup(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDeleteBackup_MissingID(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodDelete, "/admin/backups/", nil)
	rctx := chi.NewRouteContext()
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.DeleteBackup(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRestoreBackup_MissingID(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/admin/backups//restore", nil)
	rctx := chi.NewRouteContext()
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.RestoreBackup(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestContainsPathUnsafeChars(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"backup-123", false},
		{"valid_id", false},
		{"", false},
		{"../../etc", true},
		{"/absolute/path", true},
		{"dir/traversal", true},
		{"~home", true},
		{"back\\slash", true},
	}
	for _, tt := range tests {
		got := containsPathUnsafeChars(tt.input)
		if got != tt.want {
			t.Errorf("containsPathUnsafeChars(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestHandler_respondJSON(t *testing.T) {
	h := &Handler{}
	w := httptest.NewRecorder()

	data := map[string]string{"status": "ok"}
	h.respondJSON(w, http.StatusOK, data)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
}

func TestHandler_respondJSON_StatusCodes(t *testing.T) {
	h := &Handler{}
	for _, code := range []int{http.StatusOK, http.StatusAccepted, http.StatusBadRequest} {
		w := httptest.NewRecorder()
		h.respondJSON(w, code, map[string]string{"k": "v"})
		assert.Equal(t, code, w.Code)
		assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
	}
}

func TestHandler_respondJSON_ValidJSON(t *testing.T) {
	h := &Handler{}
	w := httptest.NewRecorder()
	h.respondJSON(w, http.StatusOK, map[string]interface{}{"backups": []string{"a", "b"}})

	var out map[string]interface{}
	assert.NoError(t, json.NewDecoder(w.Body).Decode(&out))
	assert.Contains(t, out, "backups")
}

func TestExtractBackupID_PathTraversal(t *testing.T) {
	h := &Handler{}
	req := reqWithID(http.MethodGet, "/admin/backups/../../etc/passwd", "../../etc/passwd")
	_, ok := h.extractBackupID(req)
	assert.False(t, ok, "path traversal should be rejected")
}

func TestExtractBackupID_ValidID(t *testing.T) {
	h := &Handler{}
	req := reqWithID(http.MethodGet, "/admin/backups/backup-2026", "backup-2026")
	id, ok := h.extractBackupID(req)
	assert.True(t, ok)
	assert.Equal(t, "backup-2026", id)
}

func TestHandler_extractBackupID(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		want   string
		wantOK bool
	}{
		{
			name:   "valid ID",
			path:   "/api/v1/admin/backups/backup-123",
			want:   "backup-123",
			wantOK: true,
		},
		{
			name:   "missing ID",
			path:   "/api/v1/admin/backups/",
			want:   "",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			rctx := chi.NewRouteContext()
			if tt.wantOK {
				rctx.URLParams.Add("id", tt.want)
			}
			ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
			req = req.WithContext(ctx)

			h := &Handler{}
			got, ok := h.extractBackupID(req)

			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}
