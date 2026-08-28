package httpapi

import (
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/delivery"
	"github.com/vidra/vidra-core/internal/ipfsmirror"
)

// handleGetVideoStoryboardImage serves a video's storyboard sprite sheet
// (kind='storyboard'). Same visibility as the detail endpoint (private → owner
// only, blocked → moderators only); a video without a stored storyboard is 404.
func (s *Server) handleGetVideoStoryboardImage(c echo.Context) error {
	return s.serveVideoStoryboard(c, "storyboard", "image/jpeg")
}

// handleGetVideoStoryboardVTT serves a video's storyboard WebVTT sprite map
// (kind='storyboard_vtt'). Same visibility as the sprite sheet.
func (s *Server) handleGetVideoStoryboardVTT(c echo.Context) error {
	return s.serveVideoStoryboard(c, "storyboard_vtt", "text/vtt")
}

// serveVideoStoryboard is the shared visibility-gated storyboard file server.
func (s *Server) serveVideoStoryboard(c echo.Context, kind, contentType string) error {
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	v, err := s.videoVisibleForMedia(c, id)
	if err != nil {
		return err
	}
	viewerID, _, authed := principalFromContext(c)
	f, err := s.videosvc.FileForView(c.Request().Context(), id, viewerID, authed, kind)
	if err != nil {
		return videoError(err)
	}
	// The sprite sheet is opaque bytes and may be delivered from anywhere. The
	// small VTT map may NOT: its cues reference "storyboard.jpg" RELATIVELY, so
	// it only works while it is served from the application URL next to the
	// sprite's own route — which is itself free to redirect. delivery.Class
	// carries that distinction (ClassStoryboardVTT is never redirectable), and
	// the mirror class is left empty for the VTT for the same reason.
	asset := mediaAsset{
		key:         f.StorageKey,
		contentType: contentType,
		class:       delivery.ClassStoryboardVTT,
		eligible:    publicVideoForIPFS(v.Privacy, v.State),
		notFound:    "video not found",
	}
	if kind == "storyboard" {
		asset.class = delivery.ClassStoryboard
		asset.mirrorClass = ipfsmirror.ClassStoryboard
		// The sprite's type is whatever its video_files row RECORDS, not whatever
		// this route was written assuming. Locally rendered sheets and the ones the
		// PeerTube importer carries across are both JPEG today, so nothing is
		// currently served wrong — but the row is the source of truth for every
		// other stored file, and a sheet that ever arrives as anything else should
		// be labelled as what it is rather than as what image/jpeg claims. Rows
		// with an empty content_type keep the constant.
		if ct := strings.TrimSpace(f.ContentType); ct != "" {
			asset.contentType = ct
		}
	}
	return s.serveMediaAsset(c, asset)
}
