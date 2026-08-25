package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/block"
)

// blockedUserView is the projection of a blocked account in the caller's block list.
type blockedUserView struct {
	UserID      string    `json:"user_id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	BlockedAt   time.Time `json:"blocked_at"`
}

// blockedUserListResponse is the paginated list of accounts the caller has blocked.
type blockedUserListResponse struct {
	Users []blockedUserView `json:"users"`
	pageMeta
}

// handleBlockUser blocks another account for the caller. Behind requireAuth.
// Blocking yourself → 422; an unknown target → 404. Idempotent.
func (s *Server) handleBlockUser(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	targetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}
	if err := s.blocksvc.Block(c.Request().Context(), userID, targetID); err != nil {
		switch {
		case errors.Is(err, block.ErrCannotBlockSelf):
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "cannot block yourself")
		case errors.Is(err, block.ErrUserNotFound):
			return echo.NewHTTPError(http.StatusNotFound, "user not found")
		}
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// handleUnblockUser lifts the caller's block of another account. Behind
// requireAuth. Idempotent (unblocking a not-blocked account still succeeds).
func (s *Server) handleUnblockUser(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	targetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}
	if err := s.blocksvc.Unblock(c.Request().Context(), userID, targetID); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// handleListBlockedUsers returns the accounts the caller has blocked, newest
// block first. Behind requireAuth. Pagination via ?limit (1–100, default 20)/?offset.
func (s *Server) handleListBlockedUsers(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.blocksvc.List(c.Request().Context(), userID, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]blockedUserView, 0, len(items))
	for _, it := range items {
		views = append(views, blockedUserView{
			UserID:      it.UserID.String(),
			Username:    it.Username,
			DisplayName: it.DisplayName,
			BlockedAt:   it.BlockedAt,
		})
	}
	return c.JSON(http.StatusOK, blockedUserListResponse{Users: views, pageMeta: page.meta(total)})
}
