package httpapi

import (
	"errors"
	"net/http"
	"strconv"
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

// videoListResponse wraps a list of videos.
type videoListResponse struct {
	Videos []videoView `json:"videos"`
}

// videoFeedResponse is the paginated cross-channel public feed.
type videoFeedResponse struct {
	Videos []videoView `json:"videos"`
	Limit  int         `json:"limit"`
	Offset int         `json:"offset"`
}

const (
	defaultVideoFeedLimit = 20
	maxVideoFeedLimit     = 100
)

// handleListPublicVideos returns the public, newest-first cross-channel feed.
// No auth required. Pagination via ?limit (1–100, default 20) and ?offset (>=0).
func (s *Server) handleListPublicVideos(c echo.Context) error {
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	vids, err := s.videosvc.ListPublic(c.Request().Context(), int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]videoView, 0, len(vids))
	for _, v := range vids {
		views = append(views, newVideoView(v))
	}
	return c.JSON(http.StatusOK, videoFeedResponse{Videos: views, Limit: limit, Offset: offset})
}

// maxSearchQueryLen bounds the search term to keep queries cheap.
const maxSearchQueryLen = 100

// videoSearchResponse is the paginated result of a public title search.
type videoSearchResponse struct {
	Query  string      `json:"query"`
	Videos []videoView `json:"videos"`
	Limit  int         `json:"limit"`
	Offset int         `json:"offset"`
}

// handleSearchVideos searches public video titles. No auth required. Requires a
// non-empty ?q (<=100 chars); paginated via ?limit (1–100, default 20)/?offset.
func (s *Server) handleSearchVideos(c echo.Context) error {
	q := strings.TrimSpace(c.QueryParam("q"))
	if q == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "query parameter q is required")
	}
	if len(q) > maxSearchQueryLen {
		return echo.NewHTTPError(http.StatusBadRequest, "query parameter q is too long")
	}
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	vids, err := s.videosvc.SearchPublic(c.Request().Context(), q, int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]videoView, 0, len(vids))
	for _, v := range vids {
		views = append(views, newVideoView(v))
	}
	return c.JSON(http.StatusOK, videoSearchResponse{Query: q, Videos: views, Limit: limit, Offset: offset})
}

// queryInt reads an integer query param, returning def when absent or malformed.
func queryInt(c echo.Context, name string, def int) int {
	raw := c.QueryParam(name)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

// clampInt bounds v to [lo, hi].
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// handleListChannelVideos lists a channel's videos. Behind optionalAuth: the
// channel owner sees all of their videos; everyone else sees only public ones.
func (s *Server) handleListChannelVideos(c echo.Context) error {
	ctx := c.Request().Context()
	ch, err := s.channelsvc.GetByHandle(ctx, c.Param("handle"))
	if err != nil {
		return channelError(err) // ErrNotFound -> 404
	}

	var vids []sqlcgen.Video
	if userID, _, ok := principalFromContext(c); ok && userID == ch.OwnerID {
		vids, err = s.videosvc.ListByChannel(ctx, ch.ID)
	} else {
		vids, err = s.videosvc.ListPublicByChannel(ctx, ch.ID)
	}
	if err != nil {
		return err
	}
	views := make([]videoView, 0, len(vids))
	for _, v := range vids {
		views = append(views, newVideoView(v))
	}
	return c.JSON(http.StatusOK, videoListResponse{Videos: views})
}

// updateVideoRequest is the PATCH /api/v1/videos/{id} body. Fields are optional;
// only those present are changed.
type updateVideoRequest struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Privacy     *string `json:"privacy"`
}

func (r updateVideoRequest) Validate() []FieldError {
	if r.Title == nil && r.Description == nil && r.Privacy == nil {
		return []FieldError{{Field: "title", Message: "at least one of title, description, privacy is required"}}
	}
	var fes []FieldError
	if r.Title != nil {
		switch n := len(strings.TrimSpace(*r.Title)); {
		case n == 0:
			fes = append(fes, FieldError{Field: "title", Message: "must not be blank"})
		case n > 200:
			fes = append(fes, FieldError{Field: "title", Message: "must be at most 200 characters"})
		}
	}
	if r.Description != nil && len(*r.Description) > 5000 {
		fes = append(fes, FieldError{Field: "description", Message: "must be at most 5000 characters"})
	}
	if r.Privacy != nil && !validVideoPrivacy[*r.Privacy] {
		fes = append(fes, FieldError{Field: "privacy", Message: "must be one of public, unlisted, private"})
	}
	return fes
}

// handleUpdateVideo updates a video owned by the authenticated user.
func (s *Server) handleUpdateVideo(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	var in updateVideoRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	v, err := s.videosvc.Update(c.Request().Context(), userID, id, video.UpdateInput{
		Title:       in.Title,
		Description: in.Description,
		Privacy:     in.Privacy,
	})
	if err != nil {
		return videoError(err)
	}
	return c.JSON(http.StatusOK, newVideoView(v))
}

// handleDeleteVideo deletes a video owned by the authenticated user.
func (s *Server) handleDeleteVideo(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if err := s.videosvc.Delete(c.Request().Context(), userID, id); err != nil {
		return videoError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// videoError maps video service sentinels to HTTP error envelopes. A non-owner
// sees 404 (not 403) so a private video's existence is not leaked; an owned but
// missing video is also 404.
func videoError(err error) error {
	switch {
	case errors.Is(err, video.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	case errors.Is(err, video.ErrForbidden):
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	default:
		return err
	}
}
