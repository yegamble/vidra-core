package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/profileimage"
)

// publicUserProfileView is intentionally account-facing and secret-free. The
// publishing identities owned by the account remain first-class channels.
type publicUserProfileView struct {
	ID          string        `json:"id"`
	Username    string        `json:"username"`
	DisplayName string        `json:"display_name"`
	Bio         string        `json:"bio"`
	CreatedAt   time.Time     `json:"created_at"`
	HasAvatar   bool          `json:"has_avatar"`
	HasBanner   bool          `json:"has_banner"`
	Channels    []channelView `json:"channels"`
}

// handleGetUserProfile returns an opted-in public profile. A signed-in owner
// may also view their private profile as a preview; every other private,
// inactive, or unknown lookup returns the same 404.
func (s *Server) handleGetUserProfile(c echo.Context) error {
	ctx := c.Request().Context()
	username := strings.TrimSpace(c.Param("username"))

	var id uuid.UUID
	var displayName, bio string
	var createdAt time.Time
	if viewerID, _, ok := principalFromContext(c); ok {
		if me, err := s.authsvc.UserByID(ctx, viewerID); err == nil && strings.EqualFold(me.Username, username) {
			id, username, displayName, bio, createdAt = me.ID, me.Username, me.DisplayName, me.Bio, me.CreatedAt
		}
	}
	if id == uuid.Nil {
		profile, err := s.authsvc.PublicProfileByUsername(ctx, username)
		if err != nil {
			if errors.Is(err, auth.ErrAccountNotFound) {
				return echo.NewHTTPError(http.StatusNotFound, "profile not found")
			}
			return err
		}
		id, username, displayName, bio, createdAt = profile.ID, profile.Username, profile.DisplayName, profile.Bio, profile.CreatedAt
	}

	channels, err := s.channelsvc.ListOwn(ctx, id)
	if err != nil {
		return err
	}
	views := make([]channelView, 0, len(channels))
	for _, ch := range channels {
		count, err := s.channelsvc.FollowerCount(ctx, ch.ID)
		if err != nil {
			return err
		}
		views = append(views, s.channelViewFor(ctx, ch, count))
	}

	view := publicUserProfileView{
		ID: id.String(), Username: username, DisplayName: displayName,
		Bio: bio, CreatedAt: createdAt, Channels: views,
	}
	if s.imagesvc != nil {
		view.HasAvatar = s.imagesvc.HasUserImage(ctx, id, profileimage.KindAvatar)
		view.HasBanner = s.imagesvc.HasUserImage(ctx, id, profileimage.KindBanner)
	}
	return c.JSON(http.StatusOK, view)
}
