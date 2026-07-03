package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// quotaStatusResponse is the caller's own storage picture: bytes currently
// stored (all their video files — originals, renditions, thumbnails) and the
// effective quota. quota_bytes is null when the caller is unlimited.
type quotaStatusResponse struct {
	UsedBytes  int64  `json:"used_bytes"`
	QuotaBytes *int64 `json:"quota_bytes"`
}

// handleGetMyQuota returns the authenticated user's current storage usage and
// effective quota (per-user override, else the instance default; null =
// unlimited). Behind requireAuth.
func (s *Server) handleGetMyQuota(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	st, err := s.quotasvc.Status(c.Request().Context(), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, quotaStatusResponse{UsedBytes: st.UsedBytes, QuotaBytes: st.QuotaBytes})
}
