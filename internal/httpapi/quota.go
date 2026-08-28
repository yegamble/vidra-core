package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/quota"
)

// checkUploadQuotas runs the storage-quota gates for incoming bytes: the
// total quota, then the rolling-24h daily quota (config-parity W7). The typed
// errors render as 422 quota_exceeded / daily_quota_exceeded. A nil quota
// service (unit-test servers) checks nothing.
func (s *Server) checkUploadQuotas(ctx context.Context, userID uuid.UUID, incoming int64) error {
	if s.quotasvc == nil {
		return nil
	}
	if err := s.quotasvc.CheckFits(ctx, userID, incoming); err != nil {
		if errors.Is(err, quota.ErrExceeded) {
			return &QuotaExceededError{}
		}
		return err
	}
	if err := s.quotasvc.CheckDailyFits(ctx, userID, incoming); err != nil {
		if errors.Is(err, quota.ErrDailyExceeded) {
			return &DailyQuotaExceededError{}
		}
		return err
	}
	return nil
}

// quotaStatusResponse is the caller's own storage picture: bytes currently
// stored (all their video files — originals, renditions, thumbnails) and the
// effective quota. quota_bytes is null when the caller is unlimited. The
// daily_* pair (config-parity W7) reports the rolling-24h upload window:
// daily_quota_bytes is null when no daily quota is configured (and
// daily_used_bytes is then 0 — the ledger is not queried).
type quotaStatusResponse struct {
	UsedBytes       int64  `json:"used_bytes"`
	QuotaBytes      *int64 `json:"quota_bytes"`
	DailyUsedBytes  int64  `json:"daily_used_bytes"`
	DailyQuotaBytes *int64 `json:"daily_quota_bytes"`
}

// handleGetMyQuota returns the authenticated user's current storage usage and
// effective quota (per-user override, else the instance default; null =
// unlimited), plus the rolling-24h daily upload window. Behind requireAuth.
func (s *Server) handleGetMyQuota(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	st, err := s.quotasvc.Status(c.Request().Context(), userID)
	if err != nil {
		return err
	}
	daily, err := s.quotasvc.Daily(c.Request().Context(), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, quotaStatusResponse{
		UsedBytes: st.UsedBytes, QuotaBytes: st.QuotaBytes,
		DailyUsedBytes: daily.UsedBytes, DailyQuotaBytes: daily.QuotaBytes,
	})
}
