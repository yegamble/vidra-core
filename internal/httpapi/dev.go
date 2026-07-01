package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/auth"
)

// handleDevEmailToken returns the most recent captured account-security token for
// an email, for the DEVELOPMENT-ONLY mail-capture flow (see WithDevMailCapture).
// It is registered only when DEV_MAIL_CAPTURE_ENABLED wired the capture mailer.
//
//	GET /api/v1/dev/email-token?email=<email>&kind=reset|verification
//	200 {"token":"..."} | 404 when nothing captured | 422 on a bad request
//
// This deliberately exposes single-use credentials and MUST never be reachable in
// production. It is intentionally not part of api/openapi.yaml (a test seam, not a
// public contract surface).
func (s *Server) handleDevEmailToken(c echo.Context) error {
	if s.devMailCapture == nil { // defensive: the route isn't registered without it
		return echo.NewHTTPError(http.StatusNotFound, "not found")
	}
	email := c.QueryParam("email")
	if email == "" {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "email is required")
	}
	kind := auth.TokenKind(c.QueryParam("kind"))
	if kind == "" {
		kind = auth.TokenKindPasswordReset
	}
	if kind != auth.TokenKindPasswordReset && kind != auth.TokenKindEmailVerification {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "kind must be 'reset' or 'verification'")
	}
	token, ok := s.devMailCapture.Latest(kind, email)
	if !ok {
		return echo.NewHTTPError(http.StatusNotFound, "no captured token for that email")
	}
	return c.JSON(http.StatusOK, map[string]string{"token": token})
}
