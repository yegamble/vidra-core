package user

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	chi "github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
)

// ---------------------------------------------------------------------------
// Mock repository
// ---------------------------------------------------------------------------

type mockArchiveRepo struct {
	export       *domain.UserExport
	exports      []*domain.UserExport
	importRecord *domain.UserImport
	err          error
}

func (m *mockArchiveRepo) CreateExport(_ context.Context, _ string) (*domain.UserExport, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.export, nil
}

func (m *mockArchiveRepo) ListExports(_ context.Context, _ string) ([]*domain.UserExport, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.exports, nil
}

func (m *mockArchiveRepo) DeleteExport(_ context.Context, _ int64, _ string) error {
	return m.err
}

func (m *mockArchiveRepo) CreateImport(_ context.Context, _ string) (*domain.UserImport, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.importRecord, nil
}

func (m *mockArchiveRepo) GetLatestImport(_ context.Context, _ string) (*domain.UserImport, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.importRecord, nil
}

func (m *mockArchiveRepo) DeleteImport(_ context.Context, _ string) error {
	return m.err
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func withUserContext(r *http.Request, userID string) *http.Request {
	ctx := context.WithValue(r.Context(), middleware.UserIDKey, userID)
	return r.WithContext(ctx)
}

func withChiParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func withChiParams(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// ---------------------------------------------------------------------------
// RequestExport tests
// ---------------------------------------------------------------------------

func TestRequestExport_OK(t *testing.T) {
	repo := &mockArchiveRepo{
		export: &domain.UserExport{ID: 1, UserID: "user-1", State: int(domain.UserExportStatePending)},
	}
	h := NewArchiveHandlers(repo)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/user-1/exports/request", nil)
	req = withUserContext(req, "user-1")
	req = withChiParam(req, "userId", "user-1")
	rr := httptest.NewRecorder()

	h.RequestExport(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp struct {
		Data domain.UserExport `json:"data"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, int64(1), resp.Data.ID)
}

func TestRequestExport_Unauthorized(t *testing.T) {
	h := NewArchiveHandlers(&mockArchiveRepo{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/user-1/exports/request", nil)
	req = withChiParam(req, "userId", "user-1")
	rr := httptest.NewRecorder()

	h.RequestExport(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequestExport_Forbidden(t *testing.T) {
	h := NewArchiveHandlers(&mockArchiveRepo{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/user-1/exports/request", nil)
	req = withUserContext(req, "other-user")
	req = withChiParam(req, "userId", "user-1")
	rr := httptest.NewRecorder()

	h.RequestExport(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

// ---------------------------------------------------------------------------
// ListExports tests
// ---------------------------------------------------------------------------

func TestListExports_OK(t *testing.T) {
	repo := &mockArchiveRepo{
		exports: []*domain.UserExport{
			{ID: 1, UserID: "user-1", State: int(domain.UserExportStateCompleted)},
			{ID: 2, UserID: "user-1", State: int(domain.UserExportStatePending)},
		},
	}
	h := NewArchiveHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/user-1/exports", nil)
	req = withUserContext(req, "user-1")
	req = withChiParam(req, "userId", "user-1")
	rr := httptest.NewRecorder()

	h.ListExports(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp struct {
		Data []*domain.UserExport `json:"data"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Len(t, resp.Data, 2)
}

func TestListExports_Empty(t *testing.T) {
	repo := &mockArchiveRepo{exports: nil}
	h := NewArchiveHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/user-1/exports", nil)
	req = withUserContext(req, "user-1")
	req = withChiParam(req, "userId", "user-1")
	rr := httptest.NewRecorder()

	h.ListExports(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp struct {
		Data []*domain.UserExport `json:"data"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Empty(t, resp.Data)
}

// ---------------------------------------------------------------------------
// DeleteExport tests
// ---------------------------------------------------------------------------

func TestDeleteExport_OK(t *testing.T) {
	repo := &mockArchiveRepo{}
	h := NewArchiveHandlers(repo)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/user-1/exports/1", nil)
	req = withUserContext(req, "user-1")
	req = withChiParams(req, map[string]string{"userId": "user-1", "id": "1"})
	rr := httptest.NewRecorder()

	h.DeleteExport(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestDeleteExport_InvalidID(t *testing.T) {
	h := NewArchiveHandlers(&mockArchiveRepo{})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/user-1/exports/abc", nil)
	req = withUserContext(req, "user-1")
	req = withChiParams(req, map[string]string{"userId": "user-1", "id": "abc"})
	rr := httptest.NewRecorder()

	h.DeleteExport(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ---------------------------------------------------------------------------
// InitImportResumable tests
// ---------------------------------------------------------------------------

func TestInitImportResumable_OK(t *testing.T) {
	repo := &mockArchiveRepo{
		importRecord: &domain.UserImport{ID: 1, UserID: "user-1", State: int(domain.UserImportStatePending)},
	}
	h := NewArchiveHandlers(repo)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/user-1/imports/import-resumable", nil)
	req = withUserContext(req, "user-1")
	req = withChiParam(req, "userId", "user-1")
	rr := httptest.NewRecorder()

	h.InitImportResumable(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestInitImportResumable_Unauthorized(t *testing.T) {
	h := NewArchiveHandlers(&mockArchiveRepo{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/user-1/imports/import-resumable", nil)
	req = withChiParam(req, "userId", "user-1")
	rr := httptest.NewRecorder()

	h.InitImportResumable(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// ---------------------------------------------------------------------------
// CancelImportResumable tests
// ---------------------------------------------------------------------------

func TestCancelImportResumable_OK(t *testing.T) {
	repo := &mockArchiveRepo{}
	h := NewArchiveHandlers(repo)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/user-1/imports/import-resumable", nil)
	req = withUserContext(req, "user-1")
	req = withChiParam(req, "userId", "user-1")
	rr := httptest.NewRecorder()

	h.CancelImportResumable(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)
}

// ---------------------------------------------------------------------------
// GetLatestImport tests
// ---------------------------------------------------------------------------

func TestGetLatestImport_OK(t *testing.T) {
	repo := &mockArchiveRepo{
		importRecord: &domain.UserImport{ID: 1, UserID: "user-1", State: int(domain.UserImportStateCompleted)},
	}
	h := NewArchiveHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/user-1/imports/latest", nil)
	req = withUserContext(req, "user-1")
	req = withChiParam(req, "userId", "user-1")
	rr := httptest.NewRecorder()

	h.GetLatestImport(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestGetLatestImport_NotFound(t *testing.T) {
	repo := &mockArchiveRepo{err: domain.ErrNotFound}
	h := NewArchiveHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/user-1/imports/latest", nil)
	req = withUserContext(req, "user-1")
	req = withChiParam(req, "userId", "user-1")
	rr := httptest.NewRecorder()

	h.GetLatestImport(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}
