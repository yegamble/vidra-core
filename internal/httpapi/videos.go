package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// validVideoPrivacy is the allowed privacy set; empty defaults to "private".
var validVideoPrivacy = map[string]bool{"public": true, "unlisted": true, "private": true}

// createVideoRequest is the POST /api/v1/channels/{handle}/videos body.
type createVideoRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Privacy     string `json:"privacy"`
}

func (r createVideoRequest) Validate() []FieldError {
	var fes []FieldError
	switch n := len(strings.TrimSpace(r.Title)); {
	case n == 0:
		fes = append(fes, FieldError{Field: "title", Message: "is required"})
	case n > 200:
		fes = append(fes, FieldError{Field: "title", Message: "must be at most 200 characters"})
	}
	if len(r.Description) > 5000 {
		fes = append(fes, FieldError{Field: "description", Message: "must be at most 5000 characters"})
	}
	if r.Privacy != "" && !validVideoPrivacy[r.Privacy] {
		fes = append(fes, FieldError{Field: "privacy", Message: "must be one of public, unlisted, private"})
	}
	return fes
}

// videoView is the public projection of a video.
type videoView struct {
	ID          string    `json:"id"`
	ChannelID   string    `json:"channel_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Privacy     string    `json:"privacy"`
	State       string    `json:"state"`
	CreatedAt   time.Time `json:"created_at"`
}

func newVideoView(v sqlcgen.Video) videoView {
	return videoView{
		ID:          v.ID.String(),
		ChannelID:   v.ChannelID.String(),
		Title:       v.Title,
		Description: v.Description,
		Privacy:     v.Privacy,
		State:       v.State,
		CreatedAt:   v.CreatedAt,
	}
}

func videoViewFromRow(v sqlcgen.GetVideoByIDRow) videoView {
	return videoView{
		ID:          v.ID.String(),
		ChannelID:   v.ChannelID.String(),
		Title:       v.Title,
		Description: v.Description,
		Privacy:     v.Privacy,
		State:       v.State,
		CreatedAt:   v.CreatedAt,
	}
}

// handleCreateVideo creates a draft video under a channel owned by the caller.
func (s *Server) handleCreateVideo(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var in createVideoRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}

	ctx := c.Request().Context()
	ch, err := s.channelsvc.GetByHandle(ctx, c.Param("handle"))
	if err != nil {
		return channelError(err) // ErrNotFound -> 404
	}
	if ch.OwnerID != userID {
		return echo.NewHTTPError(http.StatusForbidden, "you do not own this channel")
	}

	privacy := in.Privacy
	if privacy == "" {
		privacy = "private"
	}
	v, err := s.videosvc.CreateDraft(ctx, ch.ID, video.CreateInput{
		Title:       in.Title,
		Description: in.Description,
		Privacy:     privacy,
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, newVideoView(v))
}

// handleGetVideo returns a video by id. Runs behind optionalAuth: public and
// unlisted videos are visible to anyone with the link; a private video is
// visible only to its owner, and is reported as 404 (not 403) to everyone else
// so its existence is not leaked.
func (s *Server) handleGetVideo(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	v, err := s.videosvc.GetByID(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, video.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "video not found")
		}
		return err
	}
	if v.Privacy == "private" {
		userID, _, ok := principalFromContext(c)
		if !ok || userID != v.OwnerID {
			return echo.NewHTTPError(http.StatusNotFound, "video not found")
		}
	}
	return c.JSON(http.StatusOK, videoViewFromRow(v))
}
