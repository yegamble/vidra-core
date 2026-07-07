package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/video"
)

// embedPrivacyView is the GET/PUT response: the status tier plus, for the
// "whitelist" tier only, the allow-listed hostnames.
type embedPrivacyView struct {
	Status         string   `json:"status"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
}

// setEmbedPrivacyRequest is the PUT /videos/{id}/embed-privacy body.
type setEmbedPrivacyRequest struct {
	Status         string   `json:"status"`
	AllowedDomains []string `json:"allowed_domains"`
}

// embedPrivacyResponse projects the domain policy to the API shape: allowed_domains
// is present (and never null) only for the whitelist tier.
func embedPrivacyResponse(p video.EmbedPrivacy) embedPrivacyView {
	view := embedPrivacyView{Status: p.Status}
	if p.Status == video.EmbedWhitelist {
		if p.AllowedDomains == nil {
			view.AllowedDomains = []string{}
		} else {
			view.AllowedDomains = p.AllowedDomains
		}
	}
	return view
}

// handleGetVideoEmbedPrivacy returns a video's embed-privacy policy. Optional
// auth; same BASE visibility as the detail (private → owner only, blocked → mod,
// quarantined/scheduled gated, else 404) but WITHOUT the password gate — the embed
// page needs the policy pre-unlock for a password video to decide whether to even
// show the unlock prompt. It exposes only the status + allow-list, nothing else.
func (s *Server) handleGetVideoEmbedPrivacy(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if _, err := s.videoReadBase(c, id); err != nil {
		return err
	}
	p, err := s.videosvc.GetEmbedPrivacy(c.Request().Context(), id)
	if err != nil {
		return videoError(err)
	}
	return c.JSON(http.StatusOK, embedPrivacyResponse(p))
}

// handleSetVideoEmbedPrivacy replaces a video's embed-privacy policy. requireAuth,
// owner only (a non-owner or unknown id is 404). 400 on an unknown status, a
// "whitelist" with an empty/invalid domain list (hostnames only, ≤50), or
// allowed_domains supplied with a non-whitelist status.
func (s *Server) handleSetVideoEmbedPrivacy(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	var in setEmbedPrivacyRequest
	if err := c.Bind(&in); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	p, err := s.videosvc.SetEmbedPrivacy(c.Request().Context(), userID, id, video.EmbedPrivacy{
		Status:         in.Status,
		AllowedDomains: in.AllowedDomains,
	})
	if err != nil {
		var ve *video.EmbedValidationError
		if errors.As(err, &ve) {
			return echo.NewHTTPError(http.StatusBadRequest, ve.Message)
		}
		return videoError(err)
	}
	return c.JSON(http.StatusOK, embedPrivacyResponse(p))
}
