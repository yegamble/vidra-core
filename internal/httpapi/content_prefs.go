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
}

// updateContentPrefsRequest replaces the viewer's per-flag overrides; an empty
// value for a flag means inherit.
type updateContentPrefsRequest struct {
	SensitiveFlagPolicies map[string]string `json:"sensitive_flag_policies"`
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
	return c.JSON(http.StatusOK, contentPrefsResponse{SensitiveFlagPolicies: p.SensitiveFlagPolicies})
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
	p, err := s.viewerprefssvc.SetSensitiveFlagPolicies(c.Request().Context(), userID, in.SensitiveFlagPolicies)
	if err != nil {
		var ve *viewerprefs.ValidationError
		if errors.As(err, &ve) {
			return echo.NewHTTPError(http.StatusBadRequest, ve.Message)
		}
		return err
	}
	return c.JSON(http.StatusOK, contentPrefsResponse{SensitiveFlagPolicies: p.SensitiveFlagPolicies})
}
