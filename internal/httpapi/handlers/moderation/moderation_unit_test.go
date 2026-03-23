package moderation

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

const unitTestUserID = "11111111-1111-1111-1111-111111111111"

func withRouteParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func withRole(r *http.Request, role domain.UserRole) *http.Request {
	ctx := context.WithValue(r.Context(), middleware.UserRoleKey, string(role))
	return r.WithContext(ctx)
}

func withAuthUser(r *http.Request, userID string) *http.Request {
	return r.WithContext(withUserID(r.Context(), userID))
}

func TestEnsureRole_UsesContextRole(t *testing.T) {
	h := NewModerationHandlers(nil)

	adminReq := withRole(httptest.NewRequest(http.MethodGet, "/", nil), domain.RoleAdmin)
	adminW := httptest.NewRecorder()
	require.True(t, h.ensureRole(adminW, adminReq, domain.RoleAdmin))

	userReq := withRole(httptest.NewRequest(http.MethodGet, "/", nil), domain.RoleUser)
	userW := httptest.NewRecorder()
	require.False(t, h.ensureRole(userW, userReq, domain.RoleAdmin))
	require.Equal(t, http.StatusForbidden, userW.Code)
}

func TestCreateAbuseReport_ValidationFailures(t *testing.T) {
	h := NewModerationHandlers(nil)

	t.Run("unauthorized", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/abuse-reports", bytes.NewBufferString(`{}`))
		w := httptest.NewRecorder()

		h.CreateAbuseReport(w, req)

		require.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("invalid body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/abuse-reports", bytes.NewBufferString(`{`))
		req = withAuthUser(req, unitTestUserID)
		w := httptest.NewRecorder()

		h.CreateAbuseReport(w, req)

		require.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestUpdateAbuseReport_ValidationFailures(t *testing.T) {
	h := NewModerationHandlers(nil)

	t.Run("missing report id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/abuse-reports", bytes.NewBufferString(`{}`))
		w := httptest.NewRecorder()

		h.UpdateAbuseReport(w, req)

		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("missing auth context", func(t *testing.T) {
		req := withRouteParam(
			httptest.NewRequest(http.MethodPut, "/api/v1/admin/abuse-reports/report-1", bytes.NewBufferString(`{}`)),
			"id",
			"report-1",
		)
		w := httptest.NewRecorder()

		h.UpdateAbuseReport(w, req)

		require.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestDeleteAbuseReport_ForbiddenForNonAdmin(t *testing.T) {
	h := NewModerationHandlers(nil)
	req := withRouteParam(httptest.NewRequest(http.MethodDelete, "/api/v1/admin/abuse-reports/report-1", nil), "id", "report-1")
	req = withRole(req, domain.RoleUser)
	w := httptest.NewRecorder()

	h.DeleteAbuseReport(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestCreateBlocklistEntry_ValidationFailures(t *testing.T) {
	h := NewModerationHandlers(nil)

	t.Run("unauthorized", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/blocklist", bytes.NewBufferString(`{}`))
		w := httptest.NewRecorder()

		h.CreateBlocklistEntry(w, req)

		require.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("invalid json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/blocklist", bytes.NewBufferString(`{`))
		req = withAuthUser(req, unitTestUserID)
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.CreateBlocklistEntry(w, req)

		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid email", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/blocklist", bytes.NewBufferString(`{
			"block_type":"email",
			"blocked_value":"not-an-email",
			"reason":"spam"
		}`))
		req = withAuthUser(req, unitTestUserID)
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.CreateBlocklistEntry(w, req)

		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid ip", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/blocklist", bytes.NewBufferString(`{
			"block_type":"ip",
			"blocked_value":"999.999.999.999",
			"reason":"abuse"
		}`))
		req = withAuthUser(req, unitTestUserID)
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.CreateBlocklistEntry(w, req)

		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid domain", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/blocklist", bytes.NewBufferString(`{
			"block_type":"domain",
			"blocked_value":".bad domain",
			"reason":"abuse"
		}`))
		req = withAuthUser(req, unitTestUserID)
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.CreateBlocklistEntry(w, req)

		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid user id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/blocklist", bytes.NewBufferString(`{
			"block_type":"user",
			"blocked_value":"not-a-uuid",
			"reason":"abuse"
		}`))
		req = withAuthUser(req, unitTestUserID)
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.CreateBlocklistEntry(w, req)

		require.Equal(t, http.StatusBadRequest, w.Code)
	})
}
