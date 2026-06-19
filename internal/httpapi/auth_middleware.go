package httpapi

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// Echo context keys for the authenticated principal. Unexported so only this
// package can set them; handlers read via principalFromContext.
const (
	ctxKeyUserID = "auth.user_id"
	ctxKeyRole   = "auth.role"
)

// requireAuth authenticates the request from a Bearer access token and stores the
// principal (user ID + role) in the Echo context. Any failure — missing or
// malformed header, invalid/expired token, or unparseable subject — yields a 401
// unauthorized envelope without revealing which check failed.
func (s *Server) requireAuth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		token, ok := bearerToken(c.Request().Header.Get(echo.HeaderAuthorization))
		if !ok {
			return echo.NewHTTPError(http.StatusUnauthorized, "missing or malformed authorization header")
		}
		claims, err := s.authsvc.Parse(token)
		if err != nil {
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid or expired token")
		}
		userID, err := uuid.Parse(claims.Subject)
		if err != nil {
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid token subject")
		}
		c.Set(ctxKeyUserID, userID)
		c.Set(ctxKeyRole, claims.Role)
		return next(c)
	}
}

// requireRole restricts a route to principals holding one of the allowed roles.
// It must be chained AFTER requireAuth (which populates the principal). A request
// with no principal yields 401; an authenticated principal lacking an allowed
// role yields 403 forbidden. The role set is small and explicit per route.
func (s *Server) requireRole(allowed ...string) echo.MiddlewareFunc {
	allow := make(map[string]bool, len(allowed))
	for _, r := range allowed {
		allow[r] = true
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			_, role, ok := principalFromContext(c)
			if !ok {
				return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
			}
			if !allow[role] {
				return echo.NewHTTPError(http.StatusForbidden, "insufficient permissions")
			}
			return next(c)
		}
	}
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header.
// The scheme match is case-insensitive per RFC 7235; the token must be non-empty.
func bearerToken(header string) (string, bool) {
	const prefix = "bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}

// principalFromContext returns the authenticated user's ID and role. ok is false
// when the request did not pass through requireAuth.
func principalFromContext(c echo.Context) (id uuid.UUID, role string, ok bool) {
	id, idOK := c.Get(ctxKeyUserID).(uuid.UUID)
	role, roleOK := c.Get(ctxKeyRole).(string)
	return id, role, idOK && roleOK
}
