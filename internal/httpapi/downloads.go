package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/video"
)

// videoDownloadFileView describes one downloadable representation of a video:
// the stored original, or an HLS rendition's variant playlist. Size is omitted
// when unknown (HLS renditions are segmented, so no single size applies).
type videoDownloadFileView struct {
	Kind         string `json:"kind"` // "original" | "hls"
	URL          string `json:"url"`
	ContentType  string `json:"content_type"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	OriginalName string `json:"original_name,omitempty"`
	Height       int32  `json:"height,omitempty"`
	Width        int32  `json:"width,omitempty"`
}

// videoDownloadResponse is the download-metadata list for a video.
type videoDownloadResponse struct {
	Files []videoDownloadFileView `json:"files"`
}

// handleGetVideoDownloads returns a video's downloadable files: the original
// plus any ready HLS renditions. Behind optionalAuth: visibility mirrors the
// detail endpoint (public/unlisted to anyone, private only to its owner,
// blocked hidden from non-moderators; every other case is 404 so existence is
// not leaked). A video with no stored files yet (e.g. a draft) returns an
// empty list.
func (s *Server) handleGetVideoDownloads(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	ctx := c.Request().Context()
	v, err := s.videosvc.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, video.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "video not found")
		}
		return err
	}
	viewerID, _, authed := principalFromContext(c)
	if v.Privacy == "private" && (!authed || viewerID != v.OwnerID) {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if hidden, err := s.videoHiddenByBlock(c, id); err != nil {
		return err
	} else if hidden {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if quarantineHidesVideo(c, v.State, v.OwnerID) {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}

	files := make([]videoDownloadFileView, 0, 2)

	// The stored original (progressive download). Visibility was checked above,
	// so an error here just means no original is stored yet.
	if f, ferr := s.videosvc.FileForView(ctx, id, viewerID, authed, "original"); ferr == nil {
		entry := videoDownloadFileView{
			Kind:         "original",
			URL:          "/api/v1/videos/" + id.String() + "/original",
			ContentType:  f.ContentType,
			SizeBytes:    f.SizeBytes,
			OriginalName: f.OriginalName,
		}
		// The probed source dimensions, when known.
		if md, ok, merr := s.videosvc.GetMetadata(ctx, id); merr == nil && ok {
			if md.Height != nil {
				entry.Height = *md.Height
			}
			if md.Width != nil {
				entry.Width = *md.Width
			}
		}
		files = append(files, entry)
	}

	// Ready HLS renditions (variant playlists; players fetch segments relative
	// to them). Mirrors the detail's hls_url gating: only a ready playlist.
	if s.transcodesvc != nil {
		if sp, ok := s.transcodesvc.Playlist(ctx, id); ok && sp.State == "ready" && sp.MasterKey != "" {
			for _, r := range s.transcodesvc.Renditions(ctx, id) {
				files = append(files, videoDownloadFileView{
					Kind:        "hls",
					URL:         "/api/v1/videos/" + id.String() + "/hls/" + strconv.Itoa(int(r.Height)) + "p/playlist.m3u8",
					ContentType: contentTypeM3U8,
					Height:      r.Height,
					Width:       r.Width,
				})
			}
		}
	}

	return c.JSON(http.StatusOK, videoDownloadResponse{Files: files})
}
