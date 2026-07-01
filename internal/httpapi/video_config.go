package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/video"
)

// videoConfigResponse is the static video-metadata taxonomy the frontend reads
// to populate its create/edit dropdowns (categories, licenses, languages) and
// the privacy selector.
type videoConfigResponse struct {
	Categories []video.ConfigOption `json:"categories"`
	Licenses   []video.ConfigOption `json:"licenses"`
	Languages  []video.ConfigOption `json:"languages"`
	Privacies  []video.ConfigOption `json:"privacies"`
}

// handleVideoConfig returns the selectable video-metadata taxonomy. Public and
// static (no auth, no DB) — safe to cache.
func (s *Server) handleVideoConfig(c echo.Context) error {
	return c.JSON(http.StatusOK, videoConfigResponse{
		Categories: video.Categories,
		Licenses:   video.Licenses,
		Languages:  video.Languages,
		Privacies:  video.Privacies,
	})
}
