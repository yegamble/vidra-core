package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/playlist"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// validPlaylistVisibility is the allowed visibility set; empty defaults to "private".
var validPlaylistVisibility = map[string]bool{"public": true, "unlisted": true, "private": true}

// playlistView is the public projection of a playlist.
type playlistView struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Visibility  string    `json:"visibility"`
	VideoCount  int64     `json:"video_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func newPlaylistView(p sqlcgen.Playlist, count int64) playlistView {
	return playlistView{
		ID:          p.ID.String(),
		Title:       p.Title,
		Description: p.Description,
		Visibility:  p.Visibility,
		VideoCount:  count,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

func playlistViewRow(r sqlcgen.GetPlaylistByIDRow) playlistView {
	return playlistView{
		ID:          r.ID.String(),
		Title:       r.Title,
		Description: r.Description,
		Visibility:  r.Visibility,
		VideoCount:  r.VideoCount,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

// playlistCardView projects a playlist item as a discovery card (matching the
// feed card shape).
func playlistCardView(c playlist.VideoCard) videoView {
	views := c.Views
	has := c.HasThumbnail
	handle := c.ChannelHandle
	name := c.ChannelDisplayName
	return videoView{
		ID:                 c.ID.String(),
		ChannelID:          c.ChannelID.String(),
		Title:              c.Title,
		Description:        c.Description,
		Privacy:            c.Privacy,
		State:              c.State,
		CreatedAt:          c.CreatedAt,
		Views:              &views,
		HasThumbnail:       &has,
		ChannelHandle:      &handle,
		ChannelDisplayName: &name,
	}
}

// createPlaylistRequest is the POST /api/v1/playlists body.
type createPlaylistRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
}

func (r createPlaylistRequest) Validate() []FieldError {
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
	if r.Visibility != "" && !validPlaylistVisibility[r.Visibility] {
		fes = append(fes, FieldError{Field: "visibility", Message: "must be one of public, unlisted, private"})
	}
	return fes
}

// handleCreatePlaylist creates a playlist owned by the authenticated user.
func (s *Server) handleCreatePlaylist(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var in createPlaylistRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	visibility := in.Visibility
	if visibility == "" {
		visibility = "private"
	}
	p, err := s.playlistsvc.Create(c.Request().Context(), userID, playlist.CreateInput{
		Title:       in.Title,
		Description: in.Description,
		Visibility:  visibility,
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, newPlaylistView(p, 0))
}

// playlistListResponse wraps a list of playlists.
type playlistListResponse struct {
	Playlists []playlistView `json:"playlists"`
}

// handleListMyPlaylists lists the authenticated user's playlists, newest first.
func (s *Server) handleListMyPlaylists(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	rows, err := s.playlistsvc.ListOwn(c.Request().Context(), userID)
	if err != nil {
		return err
	}
	views := make([]playlistView, 0, len(rows))
	for _, r := range rows {
		views = append(views, playlistView{
			ID:          r.ID.String(),
			Title:       r.Title,
			Description: r.Description,
			Visibility:  r.Visibility,
			VideoCount:  r.VideoCount,
			CreatedAt:   r.CreatedAt,
			UpdatedAt:   r.UpdatedAt,
		})
	}
	return c.JSON(http.StatusOK, playlistListResponse{Playlists: views})
}

// playlistDetailResponse is a playlist plus its ordered video cards. The
// playlist fields are flattened alongside the videos array.
type playlistDetailResponse struct {
	playlistView
	Videos []videoView `json:"videos"`
}

// handleGetPlaylist returns a playlist and its videos. Behind optionalAuth:
// public/unlisted playlists are visible to anyone; a private playlist is visible
// only to its owner and is reported as 404 to everyone else.
func (s *Server) handleGetPlaylist(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "playlist not found")
	}
	ctx := c.Request().Context()
	p, err := s.playlistsvc.GetByID(ctx, id)
	if err != nil {
		return playlistError(err)
	}
	if p.Visibility == "private" {
		userID, _, authed := principalFromContext(c)
		if !authed || userID != p.OwnerID {
			return echo.NewHTTPError(http.StatusNotFound, "playlist not found")
		}
	}
	items, err := s.playlistsvc.ListItems(ctx, id)
	if err != nil {
		return err
	}
	videos := make([]videoView, 0, len(items))
	for _, it := range items {
		videos = append(videos, playlistCardView(it))
	}
	return c.JSON(http.StatusOK, playlistDetailResponse{playlistView: playlistViewRow(p), Videos: videos})
}

// updatePlaylistRequest is the PATCH /api/v1/playlists/{id} body. Fields are
// optional; only those present are changed.
type updatePlaylistRequest struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Visibility  *string `json:"visibility"`
}

func (r updatePlaylistRequest) Validate() []FieldError {
	if r.Title == nil && r.Description == nil && r.Visibility == nil {
		return []FieldError{{Field: "title", Message: "at least one of title, description, visibility is required"}}
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
	if r.Visibility != nil && !validPlaylistVisibility[*r.Visibility] {
		fes = append(fes, FieldError{Field: "visibility", Message: "must be one of public, unlisted, private"})
	}
	return fes
}

// handleUpdatePlaylist updates a playlist owned by the authenticated user.
func (s *Server) handleUpdatePlaylist(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "playlist not found")
	}
	var in updatePlaylistRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	if _, err := s.playlistsvc.Update(ctx, userID, id, playlist.UpdateInput{
		Title:       in.Title,
		Description: in.Description,
		Visibility:  in.Visibility,
	}); err != nil {
		return playlistError(err)
	}
	// Re-read so the response carries the current video count.
	row, err := s.playlistsvc.GetByID(ctx, id)
	if err != nil {
		return playlistError(err)
	}
	return c.JSON(http.StatusOK, playlistViewRow(row))
}

// handleDeletePlaylist deletes a playlist owned by the authenticated user.
func (s *Server) handleDeletePlaylist(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "playlist not found")
	}
	if err := s.playlistsvc.Delete(c.Request().Context(), userID, id); err != nil {
		return playlistError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// addPlaylistItemRequest is the POST /api/v1/playlists/{id}/videos body.
type addPlaylistItemRequest struct {
	VideoID string `json:"video_id"`
}

func (r addPlaylistItemRequest) Validate() []FieldError {
	if strings.TrimSpace(r.VideoID) == "" {
		return []FieldError{{Field: "video_id", Message: "is required"}}
	}
	return nil
}

// handleAddPlaylistItem appends a public, published video to a playlist owned by
// the caller (idempotent). A non-public/unpublished or unknown video is 404.
func (s *Server) handleAddPlaylistItem(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "playlist not found")
	}
	var in addPlaylistItemRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	videoID, err := uuid.Parse(strings.TrimSpace(in.VideoID))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	ctx := c.Request().Context()
	// Only public, published videos can be added.
	if s.videosvc != nil {
		v, verr := s.videosvc.GetByID(ctx, videoID)
		if verr != nil || v.State != "published" || v.Privacy != "public" {
			return echo.NewHTTPError(http.StatusNotFound, "video not found")
		}
	}
	if err := s.playlistsvc.AddItem(ctx, userID, id, videoID); err != nil {
		return playlistError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// handleRemovePlaylistItem removes a video from a playlist owned by the caller
// (idempotent).
func (s *Server) handleRemovePlaylistItem(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "playlist not found")
	}
	videoID, err := uuid.Parse(c.Param("videoId"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if err := s.playlistsvc.RemoveItem(c.Request().Context(), userID, id, videoID); err != nil {
		return playlistError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// playlistError maps playlist service sentinels to HTTP error envelopes. A
// non-owner sees 404 (not 403) so a private playlist's existence is not leaked;
// an owned but missing playlist is also 404.
func playlistError(err error) error {
	switch {
	case errors.Is(err, playlist.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "playlist not found")
	case errors.Is(err, playlist.ErrForbidden):
		return echo.NewHTTPError(http.StatusNotFound, "playlist not found")
	default:
		return err
	}
}
