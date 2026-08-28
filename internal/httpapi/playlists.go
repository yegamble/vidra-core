package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/delivery"
	"github.com/vidra/vidra-core/internal/ipfsmirror"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/playlist"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// validPlaylistVisibility is the allowed visibility set; empty defaults to "private".
var validPlaylistVisibility = map[string]bool{"public": true, "unlisted": true, "private": true}

// playlistView is the public projection of a playlist.
type playlistView struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	Visibility   string    `json:"visibility"`
	VideoCount   int64     `json:"video_count"`
	HasThumbnail bool      `json:"has_thumbnail"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func newPlaylistView(p sqlcgen.Playlist, count int64) playlistView {
	return playlistView{
		ID:           p.ID.String(),
		Title:        p.Title,
		Description:  p.Description,
		Visibility:   p.Visibility,
		VideoCount:   count,
		HasThumbnail: p.ThumbnailExt != nil && *p.ThumbnailExt != "",
		CreatedAt:    p.CreatedAt,
		UpdatedAt:    p.UpdatedAt,
	}
}

func playlistViewRow(r sqlcgen.GetPlaylistByIDRow) playlistView {
	return playlistView{
		ID:           r.ID.String(),
		Title:        r.Title,
		Description:  r.Description,
		Visibility:   r.Visibility,
		VideoCount:   r.VideoCount,
		HasThumbnail: r.ThumbnailExt != nil && *r.ThumbnailExt != "",
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
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
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
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
	pageMeta
}

// handleListMyPlaylists lists the authenticated user's playlists, newest first,
// paginated via ?limit (1–100, default 20)/?offset with a true total. It
// previously returned every playlist unbounded.
func (s *Server) handleListMyPlaylists(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	rows, total, err := s.playlistsvc.ListOwn(c.Request().Context(), userID, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]playlistView, 0, len(rows))
	for _, r := range rows {
		views = append(views, playlistView{
			ID:           r.ID.String(),
			Title:        r.Title,
			Description:  r.Description,
			Visibility:   r.Visibility,
			VideoCount:   r.VideoCount,
			HasThumbnail: r.ThumbnailExt != nil && *r.ThumbnailExt != "",
			CreatedAt:    r.CreatedAt,
			UpdatedAt:    r.UpdatedAt,
		})
	}
	return c.JSON(http.StatusOK, playlistListResponse{Playlists: views, pageMeta: page.meta(total)})
}

// playlistDetailResponse is a playlist plus its ordered video cards. The
// playlist fields are flattened alongside the videos array.
type playlistDetailResponse struct {
	playlistView
	Videos []videoView `json:"videos"`
	// The item list is paginated, so total/limit/offset describe the VIDEOS, not
	// the playlist. video_count on the playlist itself is unchanged.
	pageMeta
}

// handleGetPlaylist returns a playlist and its videos. Behind optionalAuth:
// public/unlisted playlists are visible to anyone; a private playlist is visible
// only to its owner and is reported as 404 to everyone else.
func (s *Server) handleGetPlaylist(c echo.Context) error {
	id, err := pathUUID(c, "id", "playlist not found")
	if err != nil {
		return err
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
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.playlistsvc.ListItems(ctx, id, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	videos := make([]videoView, 0, len(items))
	for _, it := range items {
		videos = append(videos, playlistCardView(it))
	}
	return c.JSON(http.StatusOK, playlistDetailResponse{
		playlistView: playlistViewRow(p), Videos: videos, pageMeta: page.meta(total),
	})
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
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "playlist not found")
	if err != nil {
		return err
	}
	var in updatePlaylistRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	// Mirror the video PATCH (videos.go): snapshot the cover key only when the
	// request could flip visibility, and fire only when the update actually
	// left "public" — an ordinary title edit must purge nothing. The snapshot
	// self-fences (media_purge.go), so it is empty unless the playlist was
	// public with a cover set, i.e. unless the edge could hold anything.
	var oldCover string
	if in.Visibility != nil {
		oldCover = s.playlistCoverEdgeKey(ctx, id)
	}
	if _, err := s.playlistsvc.Update(ctx, userID, id, playlist.UpdateInput{
		Title:       in.Title,
		Description: in.Description,
		Visibility:  in.Visibility,
	}); err != nil {
		return playlistError(err)
	}
	if in.Visibility != nil && *in.Visibility != "public" {
		s.purgeEdgeKey(ctx, "playlist_cover", id, oldCover)
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
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "playlist not found")
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	// Deleting the playlist deletes the row that NAMES its cover without
	// visiting the cover handler — snapshot before, purge after the delete
	// commits (media_purge.go).
	oldCover := s.playlistCoverEdgeKey(ctx, id)
	if err := s.playlistsvc.Delete(ctx, userID, id); err != nil {
		return playlistError(err)
	}
	s.purgeEdgeKey(ctx, "playlist_cover", id, oldCover)
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
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "playlist not found")
	if err != nil {
		return err
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
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "playlist not found")
	if err != nil {
		return err
	}
	videoID, err := pathUUID(c, "videoId", "video not found")
	if err != nil {
		return err
	}
	if err := s.playlistsvc.RemoveItem(c.Request().Context(), userID, id, videoID); err != nil {
		return playlistError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// reorderPlaylistItemsRequest is the PUT /api/v1/playlists/{id}/videos body: the
// playlist's video ids in the desired order. It must list exactly the current
// items (a permutation) — a drag-reorder UI submits the full order.
type reorderPlaylistItemsRequest struct {
	VideoIDs []string `json:"video_ids"`
}

func (r reorderPlaylistItemsRequest) Validate() []FieldError {
	if r.VideoIDs == nil {
		return []FieldError{{Field: "video_ids", Message: "is required"}}
	}
	for _, s := range r.VideoIDs {
		if _, err := uuid.Parse(strings.TrimSpace(s)); err != nil {
			return []FieldError{{Field: "video_ids", Message: "must all be valid video ids"}}
		}
	}
	return nil
}

// handleReorderPlaylistItems rewrites the order of a playlist's items. Owner-only
// (a non-owner or unknown playlist is 404, hiding existence); the body must be
// exactly the playlist's current items in the new order, else 422.
func (s *Server) handleReorderPlaylistItems(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "playlist not found")
	if err != nil {
		return err
	}
	var in reorderPlaylistItemsRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	videoIDs := make([]uuid.UUID, 0, len(in.VideoIDs))
	for _, sID := range in.VideoIDs {
		vid, _ := uuid.Parse(strings.TrimSpace(sID)) // Validate() already proved each parses.
		videoIDs = append(videoIDs, vid)
	}
	if err := s.playlistsvc.ReorderItems(c.Request().Context(), userID, id, videoIDs); err != nil {
		if errors.Is(err, playlist.ErrInvalidOrder) {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "video_ids must be exactly the playlist's items, each listed once")
		}
		return playlistError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// handleSetPlaylistThumbnail stores an owner-uploaded cover image (multipart
// form field "file") for a playlist, replacing any previous cover. Behind
// requireAuth; non-owner/unknown → 404; a non-image extension → 415. The 8M
// global body limit bounds the upload.
func (s *Server) handleSetPlaylistThumbnail(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "playlist not found")
	if err != nil {
		return err
	}
	fh, err := c.FormFile("file")
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, `multipart form field "file" is required`)
	}
	f, err := fh.Open()
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	ctx := c.Request().Context()
	// Snapshot the OLD cover key before the write (media_purge.go): a same-
	// extension re-upload overwrites it in place, and an extension change is
	// exactly when the superseded key is the one the edge cached.
	oldCover := s.playlistCoverEdgeKey(ctx, id)
	if _, err := s.playlistsvc.SetThumbnail(ctx, userID, id, playlist.UploadInput{
		Filename: fh.Filename,
		Reader:   f,
	}); err != nil {
		if errors.Is(err, playlist.ErrUnsupportedMedia) {
			return echo.NewHTTPError(http.StatusUnsupportedMediaType, "cover must be a JPEG, PNG, or WebP image")
		}
		return playlistError(err)
	}
	s.purgeEdgeKey(ctx, "playlist_cover", id, oldCover)
	// Re-read so the response carries has_thumbnail=true + the current count.
	row, err := s.playlistsvc.GetByID(c.Request().Context(), id)
	if err != nil {
		return playlistError(err)
	}
	return c.JSON(http.StatusCreated, playlistViewRow(row))
}

// handleDeletePlaylistThumbnail removes a playlist's cover (owner-only,
// idempotent).
func (s *Server) handleDeletePlaylistThumbnail(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "playlist not found")
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	// Snapshot before ClearThumbnail removes blob and column (media_purge.go).
	oldCover := s.playlistCoverEdgeKey(ctx, id)
	if err := s.playlistsvc.ClearThumbnail(ctx, userID, id); err != nil {
		return playlistError(err)
	}
	s.purgeEdgeKey(ctx, "playlist_cover", id, oldCover)
	return c.NoContent(http.StatusNoContent)
}

// handleGetPlaylistThumbnail serves a playlist's cover image. Behind
// optionalAuth: public/unlisted playlists' covers are visible to anyone; a
// private playlist's cover is visible only to its owner (else 404). A playlist
// without a cover is 404.
func (s *Server) handleGetPlaylistThumbnail(c echo.Context) error {
	id, err := pathUUID(c, "id", "playlist not found")
	if err != nil {
		return err
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
	if p.ThumbnailExt == nil || *p.ThumbnailExt == "" {
		return echo.NewHTTPError(http.StatusNotFound, "playlist not found")
	}
	ct, ok := playlist.ThumbnailContentType(*p.ThumbnailExt)
	if !ok {
		ct = "application/octet-stream"
	}
	key := media.PlaylistThumbnailKey(id, *p.ThumbnailExt)
	return s.serveMediaAsset(c, mediaAsset{
		key:         key,
		contentType: ct,
		class:       delivery.ClassPlaylistCover,
		mirrorClass: ipfsmirror.ClassPlaylistCover,
		// A cover may only leave the origin for a PUBLIC playlist: unlisted
		// covers stay on the authoritative path (they are guessable-by-id but
		// not advertised), and a private one is owner-only above.
		eligible: p.Visibility == "public",
		notFound: "playlist not found",
	})
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
