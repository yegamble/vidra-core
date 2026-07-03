package httpapi

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

// Cookie-mode refresh sessions (opt-in, for browser clients).
//
// A plain JSON API client keeps the refresh token from the response body. A
// browser SPA loses in-memory state on a hard reload, so login/register/refresh
// accept {"cookie_mode": true} — or detect an existing vidra_refresh cookie —
// and then carry the rotating refresh token in an httpOnly cookie instead:
//
//   - name vidra_refresh, scoped to Path=/api/v1/auth so the browser only ever
//     sends it to the auth endpoints (refresh, logout, …), never the wider API;
//   - httpOnly (invisible to JavaScript) + SameSite=Lax (not attached to
//     cross-site POSTs, which blunts CSRF against the auth endpoints);
//   - Secure when the instance is served over https, derived from
//     PUBLIC_BASE_URL / VIDRA_ENV (see config.CookieSecure);
//   - Max-Age = the refresh TTL (JWT_REFRESH_TTL), matching the session row.
//
// In cookie mode the response body OMITS refresh_token — the cookie is the sole
// carrier, so the raw token never has to touch JavaScript-accessible storage.
// Logout and logout-all clear the cookie. See .ralph/specs/security.md.

// refreshCookieName is the httpOnly cookie carrying the rotating refresh token.
const refreshCookieName = "vidra_refresh"

// refreshCookiePath scopes the cookie to the auth route group so the browser
// never attaches the refresh token to non-auth API calls.
const refreshCookiePath = "/api/v1/auth"

// setRefreshCookie stores the (freshly rotated) refresh token in the httpOnly
// session cookie.
func (s *Server) setRefreshCookie(c echo.Context, token string) {
	c.SetCookie(&http.Cookie{
		Name:     refreshCookieName,
		Value:    token,
		Path:     refreshCookiePath,
		MaxAge:   int(s.cfg.JWTRefreshTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.CookieSecure(),
	})
}

// clearRefreshCookie expires the refresh cookie (written as Max-Age=0), used on
// logout/logout-all and when a cookie-presented token turns out to be invalid.
func (s *Server) clearRefreshCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     refreshCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.CookieSecure(),
	})
}

// refreshCookieToken returns the refresh token carried by the request's
// vidra_refresh cookie, or "" when the cookie is absent or empty.
func refreshCookieToken(c echo.Context) string {
	ck, err := c.Cookie(refreshCookieName)
	if err != nil || ck == nil {
		return ""
	}
	return strings.TrimSpace(ck.Value)
}

// resolveRefreshToken picks the refresh token for refresh/logout: an explicit
// body token always wins; otherwise the vidra_refresh cookie is used.
// fromCookie reports which source supplied it (a cookie-supplied token implies
// cookie mode).
func resolveRefreshToken(c echo.Context, bodyToken string) (token string, fromCookie bool) {
	if t := strings.TrimSpace(bodyToken); t != "" {
		return t, false
	}
	if t := refreshCookieToken(c); t != "" {
		return t, true
	}
	return "", false
}
