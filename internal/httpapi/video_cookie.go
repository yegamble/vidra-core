package httpapi

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

const (
	videoAccessCookieName = "vidra_video_access"
	videoAccessCookiePath = "/api/v1/videos/"
	ctxVideoCookieUsed    = "auth.video_cookie_used"
)

// Native video/image requests cannot attach the SPA's in-memory bearer. Cookie
// mode mirrors that short-lived access token in an HttpOnly, video-path cookie.
// Only a private/unpublished read consults it: public playback must retain its
// anonymous CDN/cache behavior, and writes must still require explicit bearers.
func (s *Server) restoreVideoReadPrincipal(c echo.Context) {
	req := c.Request()
	if s.authsvc == nil || (req.Method != http.MethodGet && req.Method != http.MethodHead) ||
		!strings.HasPrefix(req.URL.Path, videoAccessCookiePath) || req.Header.Get("Authorization") != "" {
		return
	}
	ck, err := c.Cookie(videoAccessCookieName)
	if err != nil {
		return
	}
	claims, err := s.authsvc.Parse(ck.Value)
	if err != nil {
		return
	}
	// The cookie mirrors an access token, so it inherits the same revocation
	// check: a signed-out, deactivated or deleted account must not keep reading
	// private media until the token expires.
	userID, err := s.authsvc.AuthenticateAccessToken(c.Request().Context(), claims)
	if err != nil {
		return
	}
	c.Set(ctxKeyUserID, userID)
	c.Set(ctxKeyRole, claims.Role)
	c.Set(ctxVideoCookieUsed, true)
}
