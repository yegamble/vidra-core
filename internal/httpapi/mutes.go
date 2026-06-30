package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/mute"
)

// mutedAccountView is the projection of a muted account in the caller's mute list.
type mutedAccountView struct {
	UserID      string    `json:"user_id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	MutedAt     time.Time `json:"muted_at"`
}

// mutedAccountListResponse is the paginated list of accounts the caller has muted.
type mutedAccountListResponse struct {
	Accounts []mutedAccountView `json:"accounts"`
	Limit    int                `json:"limit"`
	Offset   int                `json:"offset"`
}

// handleMuteAccount mutes another account for the caller. Behind requireAuth.
// Muting yourself → 422; an unknown target → 404. Idempotent.
func (s *Server) handleMuteAccount(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	targetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}
	if err := s.mutesvc.Mute(c.Request().Context(), userID, targetID); err != nil {
		switch {
		case errors.Is(err, mute.ErrCannotMuteSelf):
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "cannot mute yourself")
		case errors.Is(err, mute.ErrUserNotFound):
			return echo.NewHTTPError(http.StatusNotFound, "user not found")
		}
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// handleUnmuteAccount lifts the caller's mute of another account. Behind
// requireAuth. Idempotent (unmuting a not-muted account still succeeds).
func (s *Server) handleUnmuteAccount(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	targetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}
	if err := s.mutesvc.Unmute(c.Request().Context(), userID, targetID); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// handleListMutedAccounts returns the accounts the caller has muted, newest mute
// first. Behind requireAuth. Pagination via ?limit (1–100, default 20) and ?offset.
func (s *Server) handleListMutedAccounts(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	items, err := s.mutesvc.List(c.Request().Context(), userID, int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]mutedAccountView, 0, len(items))
	for _, it := range items {
		views = append(views, mutedAccountView{
			UserID:      it.UserID.String(),
			Username:    it.Username,
			DisplayName: it.DisplayName,
			MutedAt:     it.MutedAt,
		})
	}
	return c.JSON(http.StatusOK, mutedAccountListResponse{Accounts: views, Limit: limit, Offset: offset})
}
