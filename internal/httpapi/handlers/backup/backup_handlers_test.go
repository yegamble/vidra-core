package backup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
)

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

func TestHandler_respondJSON(t *testing.T) {
	h := &Handler{}
	w := httptest.NewRecorder()

	data := map[string]string{"status": "ok"}
	h.respondJSON(w, http.StatusOK, data)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
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
