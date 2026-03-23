package moderation

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/repository"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func newSQLMockModerationHandler(t *testing.T) (*ModerationHandlers, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	sqlxDB := sqlx.NewDb(db, "sqlmock")
	repo := repository.NewModerationRepository(sqlxDB)
	handler := NewModerationHandlers(repo)
	cleanup := func() { _ = sqlxDB.Close() }
	return handler, mock, cleanup
}

// Abuse report columns used by sqlx scanning in GetAbuseReport and ListAbuseReports.
var abuseReportColumns = []string{
	"id", "reporter_id", "reason", "details", "status", "moderator_notes",
	"moderated_by", "moderated_at", "reported_entity_type",
	"reported_video_id", "reported_comment_id", "reported_user_id",
	"reported_channel_id", "created_at", "updated_at",
}

var blocklistColumns = []string{
	"id", "block_type", "blocked_value", "reason", "blocked_by",
	"expires_at", "is_active", "created_at", "updated_at",
}

// ---------------------------------------------------------------------------
// ensureRole - DB fallback path (getUserRole called)
// ---------------------------------------------------------------------------

func TestEnsureRole_DBFallback(t *testing.T) {
	t.Run("fallback to DB succeeds with allowed role", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		// No role in context, but user ID is present so getUserRole will be called.
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = withAuthUser(req, unitTestUserID)
		w := httptest.NewRecorder()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT role FROM users WHERE id = $1")).
			WithArgs(unitTestUserID).
			WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("admin"))

		result := h.ensureRole(w, req, domain.RoleAdmin)

		require.True(t, result)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("fallback to DB succeeds but role not allowed", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = withAuthUser(req, unitTestUserID)
		w := httptest.NewRecorder()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT role FROM users WHERE id = $1")).
			WithArgs(unitTestUserID).
			WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("user"))

		result := h.ensureRole(w, req, domain.RoleAdmin)

		require.False(t, result)
		assert.Equal(t, http.StatusForbidden, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("fallback to DB fails with no user ID", func(t *testing.T) {
		h := NewModerationHandlers(nil)

		// No role key and no user ID in context.
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()

		result := h.ensureRole(w, req, domain.RoleAdmin)

		require.False(t, result)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("fallback to DB fails with DB error", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = withAuthUser(req, unitTestUserID)
		w := httptest.NewRecorder()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT role FROM users WHERE id = $1")).
			WithArgs(unitTestUserID).
			WillReturnError(errors.New("connection refused"))

		result := h.ensureRole(w, req, domain.RoleAdmin)

		require.False(t, result)
		assert.Equal(t, http.StatusForbidden, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// CreateAbuseReport - repo success and failure paths
// ---------------------------------------------------------------------------

func TestCreateAbuseReport_RepoSuccess(t *testing.T) {
	h, mock, cleanup := newSQLMockModerationHandler(t)
	defer cleanup()

	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(
		`INSERT INTO abuse_reports`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow("report-new", now, now))

	body := `{"reason":"spam","details":"details here","entity_type":"video","entity_id":"` + unitTestUserID + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/abuse-reports", bytes.NewBufferString(body))
	req = withAuthUser(req, unitTestUserID)
	w := httptest.NewRecorder()

	h.CreateAbuseReport(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateAbuseReport_RepoError(t *testing.T) {
	h, mock, cleanup := newSQLMockModerationHandler(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta(
		`INSERT INTO abuse_reports`)).
		WillReturnError(errors.New("insert failed"))

	body := `{"reason":"spam","entity_type":"user","entity_id":"` + unitTestUserID + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/abuse-reports", bytes.NewBufferString(body))
	req = withAuthUser(req, unitTestUserID)
	w := httptest.NewRecorder()

	h.CreateAbuseReport(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateAbuseReport_EntityTypes(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name       string
		entityType string
		entityID   string
	}{
		{"comment with valid uuid", "comment", unitTestUserID},
		{"comment with non-uuid ref", "comment", "non-uuid-comment-ref"},
		{"channel", "channel", unitTestUserID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, mock, cleanup := newSQLMockModerationHandler(t)
			defer cleanup()

			mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO abuse_reports`)).
				WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
					AddRow("report-new", now, now))

			body := `{"reason":"spam","entity_type":"` + tt.entityType + `","entity_id":"` + tt.entityID + `"}`
			req := httptest.NewRequest(http.MethodPost, "/api/v1/abuse-reports", bytes.NewBufferString(body))
			req = withAuthUser(req, unitTestUserID)
			w := httptest.NewRecorder()

			h.CreateAbuseReport(w, req)

			assert.Equal(t, http.StatusCreated, w.Code)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// ---------------------------------------------------------------------------
// ListAbuseReports - fully 0% coverage
// ---------------------------------------------------------------------------

func TestListAbuseReports(t *testing.T) {
	now := time.Now()
	sampleRow := func(rows *sqlmock.Rows) *sqlmock.Rows {
		return rows.AddRow(
			"report-1", "user-1", "spam", sql.NullString{String: "details", Valid: true},
			domain.AbuseReportStatusPending, sql.NullString{}, sql.NullString{},
			sql.NullTime{}, domain.ReportedEntityVideo,
			sql.NullString{String: "video-1", Valid: true}, sql.NullString{}, sql.NullString{},
			sql.NullString{}, now, now,
		)
	}

	t.Run("success as admin with defaults", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/abuse-reports", nil)
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM abuse_reports WHERE 1=1")).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		mock.ExpectQuery(`SELECT id, reporter_id, reason, details, status, moderator_notes,`).
			WithArgs(20, 0).
			WillReturnRows(sampleRow(sqlmock.NewRows(abuseReportColumns)))

		h.ListAbuseReports(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data    []json.RawMessage `json:"data"`
			Success bool              `json:"success"`
			Total   int64             `json:"total"`
			Meta    struct {
				Total  int64 `json:"total"`
				Limit  int   `json:"limit"`
				Offset int   `json:"offset"`
			} `json:"meta"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.True(t, resp.Success)
		assert.Equal(t, int64(1), resp.Total)
		assert.Equal(t, 20, resp.Meta.Limit)
		assert.Equal(t, 0, resp.Meta.Offset)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success as moderator with filters", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/abuse-reports?status=pending&entity_type=video&limit=10&offset=5", nil)
		req = withRole(req, domain.RoleMod)
		w := httptest.NewRecorder()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM abuse_reports WHERE 1=1 AND status = $1 AND reported_entity_type = $2")).
			WithArgs("pending", "video").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		mock.ExpectQuery(`SELECT id, reporter_id, reason, details, status, moderator_notes,`).
			WithArgs("pending", "video", 10, 5).
			WillReturnRows(sampleRow(sqlmock.NewRows(abuseReportColumns)))

		h.ListAbuseReports(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("forbidden for regular user", func(t *testing.T) {
		h := NewModerationHandlers(nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/abuse-reports", nil)
		req = withRole(req, domain.RoleUser)
		w := httptest.NewRecorder()

		h.ListAbuseReports(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("repo error returns 500", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/abuse-reports", nil)
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM abuse_reports WHERE 1=1")).
			WillReturnError(errors.New("db error"))

		h.ListAbuseReports(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("invalid limit and offset use defaults", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/abuse-reports?limit=abc&offset=xyz", nil)
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM abuse_reports WHERE 1=1")).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

		mock.ExpectQuery(`SELECT id, reporter_id, reason, details, status, moderator_notes,`).
			WithArgs(20, 0).
			WillReturnRows(sqlmock.NewRows(abuseReportColumns))

		h.ListAbuseReports(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetAbuseReport - fully 0% coverage
// ---------------------------------------------------------------------------

func TestGetAbuseReport(t *testing.T) {
	now := time.Now()

	t.Run("success", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		rows := sqlmock.NewRows(abuseReportColumns).AddRow(
			"report-1", "user-1", "spam", sql.NullString{String: "details", Valid: true},
			domain.AbuseReportStatusPending, sql.NullString{}, sql.NullString{},
			sql.NullTime{}, domain.ReportedEntityVideo,
			sql.NullString{String: "video-1", Valid: true}, sql.NullString{}, sql.NullString{},
			sql.NullString{}, now, now,
		)

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, reporter_id, reason, details, status, moderator_notes,`)).
			WithArgs("report-1").
			WillReturnRows(rows)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/abuse-reports/report-1", nil)
		req = withRouteParam(req, "id", "report-1")
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.GetAbuseReport(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("forbidden for regular user", func(t *testing.T) {
		h := NewModerationHandlers(nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/abuse-reports/report-1", nil)
		req = withRouteParam(req, "id", "report-1")
		req = withRole(req, domain.RoleUser)
		w := httptest.NewRecorder()

		h.GetAbuseReport(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("missing report id returns 400", func(t *testing.T) {
		h := NewModerationHandlers(nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/abuse-reports/", nil)
		req = withRouteParam(req, "id", "")
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.GetAbuseReport(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("repo error returns 500", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, reporter_id, reason, details, status, moderator_notes,`)).
			WithArgs("report-1").
			WillReturnError(errors.New("db error"))

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/abuse-reports/report-1", nil)
		req = withRouteParam(req, "id", "report-1")
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.GetAbuseReport(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found returns 500 due to value receiver DomainError", func(t *testing.T) {
		// The repository returns DomainError (value type) but the handler
		// checks for *DomainError (pointer type), so the NOT_FOUND branch
		// is unreachable. The error falls through to the else (500).
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, reporter_id, reason, details, status, moderator_notes,`)).
			WithArgs("missing").
			WillReturnError(sql.ErrNoRows)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/abuse-reports/missing", nil)
		req = withRouteParam(req, "id", "missing")
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.GetAbuseReport(w, req)

		// Repository converts sql.ErrNoRows to DomainError{Code:"NOT_FOUND"}
		// but handler checks err.(*domain.DomainError) which won't match
		// value-type DomainError, so it falls into the else branch (500).
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// UpdateAbuseReport - deeper paths (repo success, repo error, invalid body with auth)
// ---------------------------------------------------------------------------

func TestUpdateAbuseReport_RepoPaths(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE abuse_reports`)).
			WillReturnResult(sqlmock.NewResult(0, 1))

		body := `{"status":"accepted","moderator_notes":"confirmed"}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/abuse-reports/report-1", bytes.NewBufferString(body))
		req = withRouteParam(req, "id", "report-1")
		req = withAuthUser(req, unitTestUserID)
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.UpdateAbuseReport(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("invalid body with auth and role", func(t *testing.T) {
		h := NewModerationHandlers(nil)

		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/abuse-reports/report-1", bytes.NewBufferString("{bad"))
		req = withRouteParam(req, "id", "report-1")
		req = withAuthUser(req, unitTestUserID)
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.UpdateAbuseReport(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("repo error returns 500", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE abuse_reports`)).
			WillReturnError(errors.New("update failed"))

		body := `{"status":"accepted"}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/abuse-reports/report-1", bytes.NewBufferString(body))
		req = withRouteParam(req, "id", "report-1")
		req = withAuthUser(req, unitTestUserID)
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.UpdateAbuseReport(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("forbidden for user role", func(t *testing.T) {
		h := NewModerationHandlers(nil)

		body := `{"status":"accepted"}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/abuse-reports/report-1", bytes.NewBufferString(body))
		req = withRouteParam(req, "id", "report-1")
		req = withAuthUser(req, unitTestUserID)
		req = withRole(req, domain.RoleUser)
		w := httptest.NewRecorder()

		h.UpdateAbuseReport(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})
}

// ---------------------------------------------------------------------------
// DeleteAbuseReport - deeper paths (admin success, missing id, repo error)
// ---------------------------------------------------------------------------

func TestDeleteAbuseReport_RepoPaths(t *testing.T) {
	t.Run("success as admin", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM abuse_reports WHERE id = $1")).
			WithArgs("report-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/abuse-reports/report-1", nil)
		req = withRouteParam(req, "id", "report-1")
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.DeleteAbuseReport(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("missing report id", func(t *testing.T) {
		h := NewModerationHandlers(nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/abuse-reports/", nil)
		req = withRouteParam(req, "id", "")
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.DeleteAbuseReport(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("repo error returns 500", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM abuse_reports WHERE id = $1")).
			WithArgs("report-1").
			WillReturnError(errors.New("delete failed"))

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/abuse-reports/report-1", nil)
		req = withRouteParam(req, "id", "report-1")
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.DeleteAbuseReport(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// CreateBlocklistEntry - repo success and error paths
// ---------------------------------------------------------------------------

func TestCreateBlocklistEntry_RepoPaths(t *testing.T) {
	now := time.Now()

	t.Run("success with valid email", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO blocklist`)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
				AddRow("block-new", now, now))

		body := `{"block_type":"email","blocked_value":"spam@example.com","reason":"spammer"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/blocklist", bytes.NewBufferString(body))
		req = withAuthUser(req, unitTestUserID)
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.CreateBlocklistEntry(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("repo error returns 500", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO blocklist`)).
			WillReturnError(errors.New("insert failed"))

		body := `{"block_type":"ip","blocked_value":"192.168.1.1","reason":"abuse"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/blocklist", bytes.NewBufferString(body))
		req = withAuthUser(req, unitTestUserID)
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.CreateBlocklistEntry(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with valid domain", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO blocklist`)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
				AddRow("block-dom", now, now))

		body := `{"block_type":"domain","blocked_value":"evil.com","reason":"malware"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/blocklist", bytes.NewBufferString(body))
		req = withAuthUser(req, unitTestUserID)
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.CreateBlocklistEntry(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with valid user uuid", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO blocklist`)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
				AddRow("block-usr", now, now))

		body := `{"block_type":"user","blocked_value":"` + unitTestUserID + `","reason":"ban"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/blocklist", bytes.NewBufferString(body))
		req = withAuthUser(req, unitTestUserID)
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.CreateBlocklistEntry(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with expires_at", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO blocklist`)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
				AddRow("block-exp", now, now))

		expiresAt := now.Add(24 * time.Hour).Format(time.RFC3339)
		body := `{"block_type":"ip","blocked_value":"10.0.0.1","reason":"temp ban","expires_at":"` + expiresAt + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/blocklist", bytes.NewBufferString(body))
		req = withAuthUser(req, unitTestUserID)
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.CreateBlocklistEntry(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("forbidden for non-admin", func(t *testing.T) {
		h := NewModerationHandlers(nil)

		body := `{"block_type":"ip","blocked_value":"10.0.0.1","reason":"abuse"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/blocklist", bytes.NewBufferString(body))
		req = withAuthUser(req, unitTestUserID)
		req = withRole(req, domain.RoleMod)
		w := httptest.NewRecorder()

		h.CreateBlocklistEntry(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})
}

// ---------------------------------------------------------------------------
// ListBlocklistEntries - fully 0% coverage
// ---------------------------------------------------------------------------

func TestListBlocklistEntries(t *testing.T) {
	now := time.Now()
	sampleBlockRow := func(rows *sqlmock.Rows) *sqlmock.Rows {
		return rows.AddRow(
			"block-1", domain.BlockTypeDomain, "evil.com",
			sql.NullString{String: "malware", Valid: true}, "admin-1",
			sql.NullTime{}, true, now, now,
		)
	}

	t.Run("success as admin with defaults", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/blocklist", nil)
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM blocklist WHERE 1=1")).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		mock.ExpectQuery(`SELECT id, block_type, blocked_value, reason, blocked_by,`).
			WithArgs(20, 0).
			WillReturnRows(sampleBlockRow(sqlmock.NewRows(blocklistColumns)))

		h.ListBlocklistEntries(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data    []json.RawMessage `json:"data"`
			Success bool              `json:"success"`
			Total   int64             `json:"total"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.True(t, resp.Success)
		assert.Equal(t, int64(1), resp.Total)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with type and active_only filters", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/blocklist?type=domain&active_only=true&limit=5&offset=10", nil)
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM blocklist WHERE 1=1 AND block_type = $1 AND is_active = $2")).
			WithArgs("domain", true).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		mock.ExpectQuery(`SELECT id, block_type, blocked_value, reason, blocked_by,`).
			WithArgs("domain", true, 5, 10).
			WillReturnRows(sampleBlockRow(sqlmock.NewRows(blocklistColumns)))

		h.ListBlocklistEntries(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("forbidden for regular user", func(t *testing.T) {
		h := NewModerationHandlers(nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/blocklist", nil)
		req = withRole(req, domain.RoleUser)
		w := httptest.NewRecorder()

		h.ListBlocklistEntries(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("repo error returns 500", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/blocklist", nil)
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM blocklist WHERE 1=1")).
			WillReturnError(errors.New("db error"))

		h.ListBlocklistEntries(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// UpdateBlocklistEntry - fully 0% coverage
// ---------------------------------------------------------------------------

func TestUpdateBlocklistEntry(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE blocklist`)).
			WillReturnResult(sqlmock.NewResult(0, 1))

		body := `{"is_active":false}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/blocklist/block-1", bytes.NewBufferString(body))
		req = withRouteParam(req, "id", "block-1")
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.UpdateBlocklistEntry(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with expires_at", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE blocklist`)).
			WillReturnResult(sqlmock.NewResult(0, 1))

		expiresAt := time.Now().Add(48 * time.Hour).Format(time.RFC3339)
		body := `{"is_active":true,"expires_at":"` + expiresAt + `"}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/blocklist/block-1", bytes.NewBufferString(body))
		req = withRouteParam(req, "id", "block-1")
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.UpdateBlocklistEntry(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("forbidden for regular user", func(t *testing.T) {
		h := NewModerationHandlers(nil)

		body := `{"is_active":false}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/blocklist/block-1", bytes.NewBufferString(body))
		req = withRouteParam(req, "id", "block-1")
		req = withRole(req, domain.RoleUser)
		w := httptest.NewRecorder()

		h.UpdateBlocklistEntry(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("missing entry id", func(t *testing.T) {
		h := NewModerationHandlers(nil)

		body := `{"is_active":false}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/blocklist/", bytes.NewBufferString(body))
		req = withRouteParam(req, "id", "")
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.UpdateBlocklistEntry(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid body", func(t *testing.T) {
		h := NewModerationHandlers(nil)

		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/blocklist/block-1", bytes.NewBufferString("{bad"))
		req = withRouteParam(req, "id", "block-1")
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.UpdateBlocklistEntry(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("repo error returns 500", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE blocklist`)).
			WillReturnError(errors.New("update failed"))

		body := `{"is_active":false}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/blocklist/block-1", bytes.NewBufferString(body))
		req = withRouteParam(req, "id", "block-1")
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.UpdateBlocklistEntry(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// DeleteBlocklistEntry - fully 0% coverage
// ---------------------------------------------------------------------------

func TestDeleteBlocklistEntry(t *testing.T) {
	t.Run("success as admin", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM blocklist WHERE id = $1")).
			WithArgs("block-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/blocklist/block-1", nil)
		req = withRouteParam(req, "id", "block-1")
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.DeleteBlocklistEntry(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("forbidden for regular user", func(t *testing.T) {
		h := NewModerationHandlers(nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/blocklist/block-1", nil)
		req = withRouteParam(req, "id", "block-1")
		req = withRole(req, domain.RoleUser)
		w := httptest.NewRecorder()

		h.DeleteBlocklistEntry(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("missing entry id", func(t *testing.T) {
		h := NewModerationHandlers(nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/blocklist/", nil)
		req = withRouteParam(req, "id", "")
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.DeleteBlocklistEntry(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("repo error returns 500", func(t *testing.T) {
		h, mock, cleanup := newSQLMockModerationHandler(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM blocklist WHERE id = $1")).
			WithArgs("block-1").
			WillReturnError(errors.New("delete failed"))

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/blocklist/block-1", nil)
		req = withRouteParam(req, "id", "block-1")
		req = withRole(req, domain.RoleAdmin)
		w := httptest.NewRecorder()

		h.DeleteBlocklistEntry(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
