package httpapi

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/playersettings"
)

// playerSettingsResponse is the GET/PUT /me/player-settings body: a user's full,
// effective player defaults. Every field is always present; a fresh user's card
// preview value is resolved from the current instance default. default_speed is
// a JSON number (e.g. 1 or 1.25); default_quality is "auto" or "<height>p".
type playerSettingsResponse struct {
	AutoplayNext             bool    `json:"autoplay_next"`
	DefaultSpeed             float64 `json:"default_speed"`
	DefaultQuality           string  `json:"default_quality"`
	CaptionsDefault          bool    `json:"captions_default"`
	TheaterDefault           bool    `json:"theater_default"`
	VideoCardPreviewsEnabled bool    `json:"video_card_previews_enabled"`
}

// updatePlayerSettingsRequest is the PUT /me/player-settings body. It is a MERGE:
// every field is optional and a pointer, so an omitted field (nil) keeps its
// stored value while a present field replaces it. It intentionally does NOT
// implement Validatable — the semantic rules (speed ladder, quality shape) live
// in the player-settings service and surface as 400, per the contract. A body
// that supplies the wrong JSON type for a field fails binding (400 malformed).
type updatePlayerSettingsRequest struct {
	AutoplayNext             *bool    `json:"autoplay_next"`
	DefaultSpeed             *float64 `json:"default_speed"`
	DefaultQuality           *string  `json:"default_quality"`
	CaptionsDefault          *bool    `json:"captions_default"`
	TheaterDefault           *bool    `json:"theater_default"`
	VideoCardPreviewsEnabled *bool    `json:"video_card_previews_enabled"`
}

// playerSettingsView projects the domain settings to the API response.
func playerSettingsView(s playersettings.Settings) playerSettingsResponse {
	return playerSettingsResponse{
		AutoplayNext:             s.AutoplayNext,
		DefaultSpeed:             s.DefaultSpeed,
		DefaultQuality:           s.DefaultQuality,
		CaptionsDefault:          s.CaptionsDefault,
		TheaterDefault:           s.TheaterDefault,
		VideoCardPreviewsEnabled: s.VideoCardPreviewsEnabled,
	}
}

// handleGetPlayerSettings returns the caller's effective player settings. Behind
// requireAuth. Always 200 with the full object — a user who never saved gets the
// effective defaults (no 404).
func (s *Server) handleGetPlayerSettings(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	settings, err := s.playersettingssvc.Get(c.Request().Context(), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, playerSettingsView(settings))
}

// handleUpdatePlayerSettings applies a partial (merge) update and returns the
// full effective settings. Behind requireAuth. An omitted field keeps its stored
// value; an invalid supplied value (default_speed not on the ladder, malformed
// default_quality) is 400 and nothing is written.
func (s *Server) handleUpdatePlayerSettings(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var in updatePlayerSettingsRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	patch := playersettings.Patch{
		AutoplayNext:             in.AutoplayNext,
		DefaultSpeed:             in.DefaultSpeed,
		DefaultQuality:           in.DefaultQuality,
		CaptionsDefault:          in.CaptionsDefault,
		TheaterDefault:           in.TheaterDefault,
		VideoCardPreviewsEnabled: in.VideoCardPreviewsEnabled,
	}
	settings, err := s.playersettingssvc.Update(c.Request().Context(), userID, patch)
	if err != nil {
		var ve *playersettings.ValidationError
		if errors.As(err, &ve) {
			return echo.NewHTTPError(http.StatusBadRequest, ve.Message)
		}
		return err
	}
	return c.JSON(http.StatusOK, playerSettingsView(settings))
}
