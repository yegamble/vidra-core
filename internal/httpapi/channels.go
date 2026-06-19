package httpapi

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// handleRe constrains channel handles to a URL-safe, federation-friendly shape:
// 3–30 chars of letters, digits, underscore.
var handleRe = regexp.MustCompile(`^[A-Za-z0-9_]{3,30}$`)

// createChannelRequest is the POST /api/v1/channels body.
type createChannelRequest struct {
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

func (r createChannelRequest) Validate() []FieldError {
	var fes []FieldError
	if !handleRe.MatchString(strings.TrimSpace(r.Handle)) {
		fes = append(fes, FieldError{Field: "handle", Message: "must be 3–30 chars: letters, digits, or underscore"})
	}
	switch n := len(strings.TrimSpace(r.DisplayName)); {
	case n == 0:
		fes = append(fes, FieldError{Field: "display_name", Message: "is required"})
	case n > 50:
		fes = append(fes, FieldError{Field: "display_name", Message: "must be at most 50 characters"})
	}
	if len(r.Description) > 1000 {
		fes = append(fes, FieldError{Field: "description", Message: "must be at most 1000 characters"})
	}
	return fes
}

// channelView is the public projection of a channel.
type channelView struct {
	ID            string    `json:"id"`
	OwnerID       string    `json:"owner_id"`
	Handle        string    `json:"handle"`
	DisplayName   string    `json:"display_name"`
	Description   string    `json:"description"`
	FollowerCount int64     `json:"follower_count"`
	CreatedAt     time.Time `json:"created_at"`
}

func newChannelView(c sqlcgen.Channel, followerCount int64) channelView {
	return channelView{
		ID:            c.ID.String(),
		OwnerID:       c.OwnerID.String(),
		Handle:        c.Handle,
		DisplayName:   c.DisplayName,
		Description:   c.Description,
		FollowerCount: followerCount,
		CreatedAt:     c.CreatedAt,
	}
}

// channelListResponse wraps a list of channels.
type channelListResponse struct {
	Channels []channelView `json:"channels"`
}

// handleCreateChannel creates a channel owned by the authenticated user.
func (s *Server) handleCreateChannel(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var in createChannelRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ch, err := s.channelsvc.Create(c.Request().Context(), userID, channel.CreateInput{
		Handle:      in.Handle,
		DisplayName: in.DisplayName,
		Description: in.Description,
	})
	if err != nil {
		if errors.Is(err, channel.ErrConflict) {
			return echo.NewHTTPError(http.StatusConflict, "channel handle already taken")
		}
		return err
	}
	// A just-created channel has no followers yet.
	return c.JSON(http.StatusCreated, newChannelView(ch, 0))
}

// handleListMyChannels lists the authenticated user's channels.
func (s *Server) handleListMyChannels(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	ctx := c.Request().Context()
	chans, err := s.channelsvc.ListOwn(ctx, userID)
	if err != nil {
		return err
	}
	views := make([]channelView, 0, len(chans))
	for _, ch := range chans {
		count, err := s.channelsvc.FollowerCount(ctx, ch.ID)
		if err != nil {
			return err
		}
		views = append(views, newChannelView(ch, count))
	}
	return c.JSON(http.StatusOK, channelListResponse{Channels: views})
}

// updateChannelRequest is the PATCH /api/v1/channels/{handle} body. Fields are
// optional; only those present are changed. The handle itself is immutable.
type updateChannelRequest struct {
	DisplayName *string `json:"display_name"`
	Description *string `json:"description"`
}

func (r updateChannelRequest) Validate() []FieldError {
	var fes []FieldError
	if r.DisplayName == nil && r.Description == nil {
		fes = append(fes, FieldError{Field: "display_name", Message: "at least one of display_name, description is required"})
		return fes
	}
	if r.DisplayName != nil {
		switch n := len(strings.TrimSpace(*r.DisplayName)); {
		case n == 0:
			fes = append(fes, FieldError{Field: "display_name", Message: "must not be blank"})
		case n > 50:
			fes = append(fes, FieldError{Field: "display_name", Message: "must be at most 50 characters"})
		}
	}
	if r.Description != nil && len(*r.Description) > 1000 {
		fes = append(fes, FieldError{Field: "description", Message: "must be at most 1000 characters"})
	}
	return fes
}

// handleUpdateChannel updates a channel owned by the authenticated user.
func (s *Server) handleUpdateChannel(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var in updateChannelRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	ch, err := s.channelsvc.Update(ctx, userID, c.Param("handle"), channel.UpdateInput{
		DisplayName: in.DisplayName,
		Description: in.Description,
	})
	if err != nil {
		return channelError(err)
	}
	count, err := s.channelsvc.FollowerCount(ctx, ch.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, newChannelView(ch, count))
}

// handleDeleteChannel deletes a channel owned by the authenticated user.
func (s *Server) handleDeleteChannel(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	if err := s.channelsvc.Delete(c.Request().Context(), userID, c.Param("handle")); err != nil {
		return channelError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// channelError maps channel service sentinels to HTTP error envelopes.
func channelError(err error) error {
	switch {
	case errors.Is(err, channel.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "channel not found")
	case errors.Is(err, channel.ErrForbidden):
		return echo.NewHTTPError(http.StatusForbidden, "you do not own this channel")
	case errors.Is(err, channel.ErrConflict):
		return echo.NewHTTPError(http.StatusConflict, "channel handle already taken")
	default:
		return err
	}
}

// handleGetChannel returns a channel by its public handle. No auth required.
func (s *Server) handleGetChannel(c echo.Context) error {
	ctx := c.Request().Context()
	ch, err := s.channelsvc.GetByHandle(ctx, c.Param("handle"))
	if err != nil {
		if errors.Is(err, channel.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "channel not found")
		}
		return err
	}
	count, err := s.channelsvc.FollowerCount(ctx, ch.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, newChannelView(ch, count))
}

// handleFollowChannel makes the authenticated user follow a channel. Idempotent.
func (s *Server) handleFollowChannel(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	if err := s.channelsvc.Follow(c.Request().Context(), userID, c.Param("handle")); err != nil {
		return channelError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// handleUnfollowChannel removes the authenticated user's follow. Idempotent.
func (s *Server) handleUnfollowChannel(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	if err := s.channelsvc.Unfollow(c.Request().Context(), userID, c.Param("handle")); err != nil {
		return channelError(err)
	}
	return c.NoContent(http.StatusNoContent)
}
