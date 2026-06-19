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
	ID          string    `json:"id"`
	OwnerID     string    `json:"owner_id"`
	Handle      string    `json:"handle"`
	DisplayName string    `json:"display_name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

func newChannelView(c sqlcgen.Channel) channelView {
	return channelView{
		ID:          c.ID.String(),
		OwnerID:     c.OwnerID.String(),
		Handle:      c.Handle,
		DisplayName: c.DisplayName,
		Description: c.Description,
		CreatedAt:   c.CreatedAt,
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
	return c.JSON(http.StatusCreated, newChannelView(ch))
}

// handleListMyChannels lists the authenticated user's channels.
func (s *Server) handleListMyChannels(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	chans, err := s.channelsvc.ListOwn(c.Request().Context(), userID)
	if err != nil {
		return err
	}
	views := make([]channelView, 0, len(chans))
	for _, ch := range chans {
		views = append(views, newChannelView(ch))
	}
	return c.JSON(http.StatusOK, channelListResponse{Channels: views})
}

// handleGetChannel returns a channel by its public handle. No auth required.
func (s *Server) handleGetChannel(c echo.Context) error {
	ch, err := s.channelsvc.GetByHandle(c.Request().Context(), c.Param("handle"))
	if err != nil {
		if errors.Is(err, channel.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "channel not found")
		}
		return err
	}
	return c.JSON(http.StatusOK, newChannelView(ch))
}
