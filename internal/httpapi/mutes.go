package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/mute"
)

// mutedAccountView is the projection of a muted account in the caller's mute list.
type mutedAccountView struct {
	UserID      string    `json:"user_id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	MutedAt     time.Time `json:"muted_at"`
	// ChannelHandles: the handles this account publishes under. No `omitempty`
	// — the client intersects a set against it, so an account with no channel
	// must marshal `[]` and never disappear from the shape.
	ChannelHandles []string `json:"channel_handles"`
}

// mutedAccountListResponse is the paginated list of accounts the caller has muted.
type mutedAccountListResponse struct {
	Accounts []mutedAccountView `json:"accounts"`
	pageMeta
}

// handleMuteAccount mutes another account for the caller. Behind requireAuth.
// Muting yourself → 422; an unknown target → 404. Idempotent.
func (s *Server) handleMuteAccount(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	targetID, err := pathUUID(c, "id", "user not found")
	if err != nil {
		return err
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
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	targetID, err := pathUUID(c, "id", "user not found")
	if err != nil {
		return err
	}
	if err := s.mutesvc.Unmute(c.Request().Context(), userID, targetID); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// handleListMutedAccounts returns the accounts the caller has muted, newest mute
// first. Behind requireAuth. Pagination via ?limit (1–100, default 20) and ?offset.
func (s *Server) handleListMutedAccounts(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.mutesvc.List(c.Request().Context(), userID, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]mutedAccountView, 0, len(items))
	for _, it := range items {
		views = append(views, mutedAccountView{
			UserID:         it.UserID.String(),
			Username:       it.Username,
			DisplayName:    it.DisplayName,
			MutedAt:        it.MutedAt,
			ChannelHandles: it.ChannelHandles,
		})
	}
	return c.JSON(http.StatusOK, mutedAccountListResponse{Accounts: views, pageMeta: page.meta(total)})
}
