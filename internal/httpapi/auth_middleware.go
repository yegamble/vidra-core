package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/admin"
)

// Echo context keys for the authenticated principal. Unexported so only this
// package can set them; handlers read via principalFromContext.
const (
	ctxKeyUserID         = "auth.user_id"
	ctxKeyRole           = "auth.role"
	ctxKeyTokenExpiresAt = "auth.token_expires_at"
	// ctxKeySessionID is the session the access token is bound to. Handlers that
	// revoke sessions need it so they can spare the caller's own.
	ctxKeySessionID = "auth.session_id"
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
		// Verifying the JWT proves only that this instance minted it. The
		// session lookup is what makes revocation EFFECTIVE — without it a
		// revoked session's, a deactivated account's or a hard-deleted
		// account's unexpired access token kept working on every route that did
		// not itself load the user row, for the whole JWT_ACCESS_TTL. One
		// indexed read; see auth.Service.AuthenticateAccessToken.
		principal, err := s.authsvc.AuthenticateAccessToken(c.Request().Context(), claims)
		if err != nil {
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid or expired token")
		}
		c.Set(ctxKeyUserID, principal.UserID)
		// The ROLE comes from that same account row, not from the token's copy
		// of it, so a role change reaches a session already in flight.
		c.Set(ctxKeyRole, principal.Role)
		c.Set(ctxKeySessionID, claims.SessionID)
		if claims.ExpiresAt != nil {
			c.Set(ctxKeyTokenExpiresAt, claims.ExpiresAt.Time)
		}
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
			_, role, err := mustPrincipal(c)
			if err != nil {
				return err
			}
			if !allow[role] {
				return echo.NewHTTPError(http.StatusForbidden, "insufficient permissions")
			}
			return next(c)
		}
	}
}

// optionalAuth populates the principal when a valid Bearer token is present but,
// unlike requireAuth, never rejects: anonymous and badly-authenticated requests
// proceed without a principal. Handlers use principalFromContext to vary
// behaviour (e.g. an owner seeing their own private resource). It is a no-op
// when no auth service is configured.
func (s *Server) optionalAuth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if s.authsvc != nil {
			if token, ok := bearerToken(c.Request().Header.Get(echo.HeaderAuthorization)); ok {
				if claims, err := s.authsvc.Parse(token); err == nil {
					// Same revocation check as requireAuth: a revoked or
					// tombstoned principal must not be a principal here either
					// (these routes vary behaviour by identity — an owner sees
					// their own private resource). Failure is silent: the
					// request proceeds anonymously, as it does for any other
					// unusable token.
					if principal, err := s.authsvc.AuthenticateAccessToken(c.Request().Context(), claims); err == nil {
						c.Set(ctxKeyUserID, principal.UserID)
						c.Set(ctxKeyRole, principal.Role)
					}
				}
			}
		}
		return next(c)
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

// mustPrincipal returns the authenticated user's ID and role, or the 401
// unauthorized error handlers behind requireAuth return when no principal is
// present. Handlers that must vary behaviour for anonymous callers (optionalAuth
// routes) or that deliberately answer 404 to avoid leaking existence use
// principalFromContext directly.
func mustPrincipal(c echo.Context) (uuid.UUID, string, error) {
	id, role, ok := principalFromContext(c)
	if !ok {
		return uuid.Nil, "", echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	return id, role, nil
}

// isStaff reports whether role is one of the two moderation roles. The pair is
// the "moderation escape" every content route consults — staff read and manage
// any local video, comment or live stream regardless of ownership — and it was
// spelled out inline under six different local names before this helper.
func isStaff(role string) bool {
	return role == admin.RoleAdmin || role == admin.RoleModerator
}

// sessionIDFromContext returns the id of the session the caller's access token
// is bound to (empty when the request did not pass through requireAuth).
func sessionIDFromContext(c echo.Context) string {
	id, _ := c.Get(ctxKeySessionID).(string)
	return id
}

// tokenExpiryFromContext returns the authenticated access-token expiry. Long
// lived streaming handlers cap their connection lifetime to this value so role
// and token validity are re-evaluated on reconnect.
func tokenExpiryFromContext(c echo.Context) (time.Time, bool) {
	expires, ok := c.Get(ctxKeyTokenExpiresAt).(time.Time)
	return expires, ok
}
