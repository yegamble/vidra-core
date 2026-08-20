package httpapi

import "github.com/labstack/echo/v4"

// secureHeaders sets conservative security response headers on every response.
// These are safe defaults for a JSON API + media server (see fix_plan P15):
//   - X-Content-Type-Options: nosniff — never MIME-sniff a response body.
//   - X-Frame-Options: DENY — the API is not meant to be framed (clickjacking).
//   - Referrer-Policy: strict-origin-when-cross-origin — don't leak full URLs.
//   - Cross-Origin-Opener-Policy: same-origin — isolate the browsing context.
//   - X-Permitted-Cross-Domain-Policies: none — no Adobe cross-domain access.
//
// HSTS is added when the instance's public origin is HTTPS
// (config.PublicOriginIsHTTPS — the same predicate that pins Secure cookies), so
// browsers pin TLS for two years. It is omitted on a plain-http origin: a
// browser ignores the header there anyway (RFC 6797 §8.1), but a deployment that
// LATER puts a certificate in front of the same hostname would have trapped
// every visitor who saw it on http, and the local-development case is the same
// trap on localhost.
//
// A Content-Security-Policy is deliberately NOT set here: this service serves
// JSON and media, not HTML, so a page CSP belongs to vidra-user. CORS remains
// handled by the dedicated CORS middleware.
func secureHeaders(httpsOrigin bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			h := c.Response().Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("X-Permitted-Cross-Domain-Policies", "none")
			if httpsOrigin {
				// 2 years, apply to subdomains.
				h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			return next(c)
		}
	}
}
