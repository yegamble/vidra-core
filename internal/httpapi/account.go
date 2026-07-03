package httpapi

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/account"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// deleteAccountRequest is the DELETE /api/v1/auth/me body: the current
// password re-confirms the irreversible action.
type deleteAccountRequest struct {
	Password string `json:"password"`
}

func (r deleteAccountRequest) Validate() []FieldError {
	if r.Password == "" {
		return []FieldError{{Field: "password", Message: "is required"}}
	}
	return nil
}

// handleDeleteAccount performs the §1 hard delete of the authenticated
// account after re-confirming its password (product-decisions.md §1):
// channels/videos are hard-deleted (federation Delete fan-out, best-effort
// blob cleanup), comments become tombstones, per-user rows are purged, all
// sessions are revoked, and the users row is anonymised. Irreversible. 204 on
// success; a wrong (or absent — OAuth-only accounts) password is 403.
func (s *Server) handleDeleteAccount(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var in deleteAccountRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if err := s.authsvc.ConfirmPassword(c.Request().Context(), userID, in.Password); err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidPassword):
			s.audit(c, observability.ActionAccountDelete, observability.ResultFailure, userID.String(), "invalid_password")
			return echo.NewHTTPError(http.StatusForbidden, "incorrect password")
		case errors.Is(err, auth.ErrAccountNotFound):
			return echo.NewHTTPError(http.StatusUnauthorized, "account no longer available")
		}
		return err
	}
	if err := s.accountsvc.Delete(c.Request().Context(), userID); err != nil {
		if errors.Is(err, account.ErrNotFound) {
			return echo.NewHTTPError(http.StatusUnauthorized, "account no longer available")
		}
		return err
	}
	s.audit(c, observability.ActionAccountDelete, observability.ResultSuccess, userID.String(), "")
	return c.NoContent(http.StatusNoContent)
}

// handleAdminDeleteUser is the admin variant of the §1 hard delete: same
// semantics, no password confirmation, self-guarded (an admin cannot delete
// their own account this way — 422). Behind requireRole(admin).
func (s *Server) handleAdminDeleteUser(c echo.Context) error {
	adminID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	targetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}
	if targetID == adminID {
		s.audit(c, observability.ActionAdminUserDelete, observability.ResultFailure, adminID.String(), "self_delete_refused")
		return &ValidationError{Fields: []FieldError{{Field: "id", Message: "you cannot hard-delete your own account; use DELETE /api/v1/auth/me"}}}
	}
	if err := s.accountsvc.Delete(c.Request().Context(), targetID); err != nil {
		if errors.Is(err, account.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "user not found")
		}
		return err
	}
	// Reason carries only the target's id — safe, no PII.
	s.audit(c, observability.ActionAdminUserDelete, observability.ResultSuccess, adminID.String(), "target="+targetID.String())
	return c.NoContent(http.StatusNoContent)
}

// accountExportView is the export-status projection shared by the request and
// status endpoints.
type accountExportView struct {
	ID    string `json:"id"`
	State string `json:"state"`
	// DownloadReady is true when GET /api/v1/me/export/download will serve the
	// archive (state done and not yet expired).
	DownloadReady bool       `json:"download_ready"`
	RequestedAt   time.Time  `json:"requested_at"`
	ExpiresAt     *time.Time `json:"expires_at"`
}

func (s *Server) accountExportView(row sqlcgen.AccountExport) accountExportView {
	v := accountExportView{
		ID:          row.ID.String(),
		State:       row.State,
		RequestedAt: row.CreatedAt,
	}
	if row.ExpiresAt.Valid {
		t := row.ExpiresAt.Time
		v.ExpiresAt = &t
		v.DownloadReady = row.State == account.ExportDone && time.Now().Before(t)
	}
	return v
}

// handleRequestAccountExport enqueues a fresh export of the caller's account
// data (P4). Single active export per user: 409 while one is pending/running;
// a finished/failed export is replaced. 202 with the queued job's status.
func (s *Server) handleRequestAccountExport(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	row, err := s.accountsvc.RequestExport(c.Request().Context(), userID)
	if err != nil {
		if errors.Is(err, account.ErrExportActive) {
			return echo.NewHTTPError(http.StatusConflict, "an export is already in progress")
		}
		return err
	}
	return c.JSON(http.StatusAccepted, s.accountExportView(row))
}

// handleGetAccountExport reports the caller's latest export status. 404 when
// none was ever requested (or the expired archive was already swept).
func (s *Server) handleGetAccountExport(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	row, err := s.accountsvc.LatestExport(c.Request().Context(), userID)
	if err != nil {
		if errors.Is(err, account.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "no export found")
		}
		return err
	}
	return c.JSON(http.StatusOK, s.accountExportView(row))
}

// handleDownloadAccountExport streams the caller's finished archive as a JSON
// attachment. 409 while the export is still pending/running or after it
// failed; 404 when none exists; 410 past its 7-day expiry.
func (s *Server) handleDownloadAccountExport(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	rc, row, err := s.accountsvc.OpenExport(c.Request().Context(), userID)
	if err != nil {
		switch {
		case errors.Is(err, account.ErrNotFound):
			return echo.NewHTTPError(http.StatusNotFound, "no export found")
		case errors.Is(err, account.ErrExportNotReady):
			return echo.NewHTTPError(http.StatusConflict, "the export is not ready for download")
		case errors.Is(err, account.ErrExportExpired):
			return echo.NewHTTPError(http.StatusGone, "the export has expired; request a new one")
		}
		return err
	}
	defer func() { _ = rc.Close() }()
	c.Response().Header().Set(echo.HeaderContentDisposition, `attachment; filename="vidra-export-`+row.ID.String()+`.json"`)
	return c.Stream(http.StatusOK, echo.MIMEApplicationJSON, rc)
}

// handleImportAccount accepts a vidra account archive (the same JSON shape the
// export produces) and re-creates the SAFE subsets for the caller: profile
// fields, playlists (items matched by video id when present locally), follows
// of local channels, and notification preferences. Everything else is reported
// as skipped in the returned summary. The request body is bounded by the
// global HTTP body limit.
func (s *Server) handleImportAccount(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	raw, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return err
	}
	archive, err := account.ParseArchive(raw)
	if err != nil {
		return &ValidationError{Fields: []FieldError{{Field: "archive", Message: "not a valid vidra account archive"}}}
	}
	summary, err := s.accountsvc.ImportArchive(c.Request().Context(), userID, archive)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, summary)
}
