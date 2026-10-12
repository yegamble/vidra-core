package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/ratelimit"
	"github.com/vidra/vidra-core/internal/urlsafety"
)

// Root well-known documents beyond NodeInfo/WebFinger. Like those they sit
// outside /api/v1, are excluded from the REST OpenAPI contract
// (knownNonContractRoutes) and mount with the federation block. Every URL is
// built from PUBLIC_BASE_URL, never from the request Host header, so a spoofed
// Host cannot poison a cached document.

// securityTxtLifetime is how far ahead Expires is set. RFC 9116 asks for
// "less than a year", and the file is recomputed per request, so it can never
// go stale.
const securityTxtLifetime = 364 * 24 * time.Hour

// handleHostMeta serves /.well-known/host-meta: the XRD pointer from which
// Mastodon-era servers discover the WebFinger endpoint (lrdd, RFC 6415).
func (s *Server) handleHostMeta(c echo.Context) error {
	tmpl := s.cfg.PublicBaseURL + "/.well-known/webfinger?resource={uri}"
	xml := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<XRD xmlns="http://docs.oasis-open.org/ns/xri/xrd-1.0">` + "\n" +
		`  <Link rel="lrdd" type="application/xrd+xml" template="` + tmpl + `"/>` + "\n" +
		`</XRD>`
	return c.Blob(http.StatusOK, "application/xrd+xml; charset=utf-8", []byte(xml))
}

// handleHostMetaJSON is the JRD form of the same pointer.
func (s *Server) handleHostMetaJSON(c echo.Context) error {
	c.Response().Header().Set(echo.HeaderContentType, "application/jrd+json; charset=utf-8")
	return c.JSON(http.StatusOK, map[string]any{"links": []map[string]string{{
		"rel":      "lrdd",
		"type":     "application/jrd+json",
		"template": s.cfg.PublicBaseURL + "/.well-known/webfinger?resource={uri}",
	}}})
}

// handleSecurityTxt serves RFC 9116 /.well-known/security.txt. The contact is
// the instance's configured contact email (setting, else INSTANCE_CONTACT_EMAIL);
// with none configured there is nothing honest to publish, so it is 404 rather
// than a made-up address.
func (s *Server) handleSecurityTxt(c echo.Context) error {
	contact := strings.TrimSpace(s.effectiveContactEmail())
	if contact == "" || strings.ContainsAny(contact, "\r\n") {
		return echo.NewHTTPError(http.StatusNotFound)
	}
	body := fmt.Sprintf("Contact: mailto:%s\nExpires: %s\nCanonical: %s/.well-known/security.txt\n",
		contact, time.Now().UTC().Add(securityTxtLifetime).Format(time.RFC3339), s.cfg.PublicBaseURL)
	return c.Blob(http.StatusOK, "text/plain; charset=utf-8", []byte(body))
}

// handleWellKnownChangePassword is the well-known change-password URL (W3C): password
// managers send the user straight to the page where the password is changed.
func (s *Server) handleWellKnownChangePassword(c echo.Context) error {
	return c.Redirect(http.StatusFound, s.cfg.PublicBaseURL+"/settings/security")
}

// remoteInteractionLimit budgets the resolve endpoint per client IP on
// the same limiter as remote-URI search resolution.
func (s *Server) remoteInteractionLimit() echo.MiddlewareFunc {
	return s.limitBy(limitRule{
		resolve: func(c echo.Context) (*ratelimit.Limiter, string, bool) {
			return s.searchResolveLimit, "remote-interaction:ip:" + c.RealIP(), true
		},
		unavailable: "remote-interaction rate limiter unavailable, failing open",
		denied:      "too many remote-interaction lookups",
	})
}

type remoteInteractionResponse struct {
	URL string `json:"url"`
}

// handleResolveRemoteInteraction is GET /remote-interaction/resolve: the
// outbound half of remote interaction. Anonymous by design (the visitor is not
// signed in here), so it is IP-rate-limited and resolves only through the
// SSRF-guarded federation machinery.
func (s *Server) handleResolveRemoteInteraction(c echo.Context) error {
	if !s.cfg.FederationEnabled {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "federation is disabled on this instance")
	}
	u, err := s.fedsvc.ResolveSubscribeURL(c.Request().Context(), c.QueryParam("handle"), c.QueryParam("target"))
	switch {
	case err == nil:
		return c.JSON(http.StatusOK, remoteInteractionResponse{URL: u})
	case errors.Is(err, federation.ErrBadInteraction):
		return &ValidationError{Fields: []FieldError{{Field: "handle", Message: "must be user@host, and target an address on this instance"}}}
	case errors.Is(err, urlsafety.ErrInvalidURL), errors.Is(err, urlsafety.ErrBlockedAddress):
		// Never echo the attacker-supplied host back.
		return &ValidationError{Fields: []FieldError{{Field: "handle", Message: "names a server this instance will not contact"}}}
	case errors.Is(err, federation.ErrNoSubscribeTemplate):
		return echo.NewHTTPError(http.StatusNotFound, "that server does not support remote interaction")
	}
	return echo.NewHTTPError(http.StatusBadGateway, "could not reach that server")
}
