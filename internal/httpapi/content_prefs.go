package httpapi

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/viewerprefs"
)

// contentPrefsResponse is the GET/PUT /me/content-preferences body.
type contentPrefsResponse struct {
	SensitiveFlagPolicies map[string]string `json:"sensitive_flag_policies"`
	PreferredLanguages    []string          `json:"preferred_languages"`
}

// updateContentPrefsRequest is a merge: an omitted field keeps its stored
// value. sensitive_flag_policies replaces the overrides (an empty value for a
// flag means inherit); preferred_languages replaces the list ([] clears).
type updateContentPrefsRequest struct {
	SensitiveFlagPolicies map[string]string `json:"sensitive_flag_policies"`
	PreferredLanguages    *[]string         `json:"preferred_languages"`
}

func (s *Server) handleGetContentPrefs(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	p, err := s.viewerprefssvc.Get(c.Request().Context(), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, contentPrefsResponse{SensitiveFlagPolicies: p.SensitiveFlagPolicies, PreferredLanguages: p.PreferredLanguages})
}

func (s *Server) handleUpdateContentPrefs(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in updateContentPrefsRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	p, err := s.viewerprefssvc.Update(c.Request().Context(), userID, viewerprefs.Patch{
		SensitiveFlagPolicies: in.SensitiveFlagPolicies, PreferredLanguages: in.PreferredLanguages,
	})
	if err != nil {
		var ve *viewerprefs.ValidationError
		if errors.As(err, &ve) {
			return echo.NewHTTPError(http.StatusBadRequest, ve.Message)
		}
		return err
	}
	return c.JSON(http.StatusOK, contentPrefsResponse{SensitiveFlagPolicies: p.SensitiveFlagPolicies, PreferredLanguages: p.PreferredLanguages})
}
