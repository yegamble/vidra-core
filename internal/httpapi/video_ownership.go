package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/video"
)

// Video ownership changes (T21, migration 0165): the owner offers a video to
// another local user, who accepts or refuses. Writes
// ride the general API rate limit every other route here does.

type giveOwnershipRequest struct {
	Username string `json:"username"`
}

func (r giveOwnershipRequest) Validate() []FieldError {
	if strings.TrimSpace(r.Username) == "" {
		return []FieldError{{Field: "username", Message: "is required"}}
	}
	return nil
}

// handleGiveVideoOwnership: POST /videos/{id}/give-ownership. Owner only; an
// unknown and an ineligible recipient (the giver included) answer the same 404. 204 on success, and
// the recipient is notified (best-effort) unless they muted the giver.
func (s *Server) handleGiveVideoOwnership(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	var in giveOwnershipRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	oc, notify, err := s.videosvc.GiveOwnership(ctx, userID, id, in.Username)
	if err != nil {
		return ownershipError(err)
	}
	if notify && s.notifsvc != nil {
		if nerr := s.notifsvc.NotifyVideoOwnershipRequest(ctx, oc.NextOwnerID, userID); nerr != nil {
			s.logger.WarnContext(ctx, "notify video ownership request failed", "error", nerr, "video_id", id)
		}
	}
	return c.NoContent(http.StatusNoContent)
}

// handleCancelVideoOwnership: DELETE /videos/{id}/give-ownership — the giver
// withdraws their pending offer. Anything else (no such video, not theirs, no
// pending offer from them) is the same 404.
func (s *Server) handleCancelVideoOwnership(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "ownership change not found")
	if err != nil {
		return err
	}
	if err := s.videosvc.CancelOwnershipChange(c.Request().Context(), userID, id); err != nil {
		return ownershipError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// ownershipError maps the ownership-change sentinels; anything else falls
// through to the video mapping (a foreign or missing video is 404).
func ownershipError(err error) error {
	switch {
	case errors.Is(err, video.ErrOwnershipUnavailable):
		return echo.NewHTTPError(http.StatusServiceUnavailable, "ownership changes are not available")
	case errors.Is(err, video.ErrRecipientNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	case errors.Is(err, video.ErrOwnershipPending):
		return echo.NewHTTPError(http.StatusConflict, "an ownership change for this video is already pending")
	case errors.Is(err, video.ErrNotSettled):
		return echo.NewHTTPError(http.StatusConflict, "the video is still uploading or processing")
	case errors.Is(err, video.ErrOwnershipCooldown):
		return echo.NewHTTPError(http.StatusConflict, "this user recently refused an offer from you; try again later")
	case errors.Is(err, video.ErrOwnershipNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "ownership change not found")
	}
	return videoError(err)
}
