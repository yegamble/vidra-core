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

// handleVideoConfig returns the selectable video-metadata taxonomy. Public, and
// no longer wholly static: categories are the INSTANCE's taxonomy, which an
// operator can replace (see instancesettings.KeyInstanceCustomCategories). The
// other three are fixed vocabularies and stay compiled in.
func (s *Server) handleVideoConfig(c echo.Context) error {
	return c.JSON(http.StatusOK, videoConfigResponse{
		// Same accessor validation uses, so what the picker offers and what a
		// write accepts cannot drift apart.
		Categories: video.CategoryOptions(),
		Licenses:   video.Licenses,
		Languages:  video.Languages,
		Privacies:  video.Privacies,
	})
}
