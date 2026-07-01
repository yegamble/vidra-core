package httpapi

import (
	"context"
	"strings"

	"github.com/labstack/echo/v4"
)

// correlationHeader is the request/response header carrying a cross-service
// correlation ID. It pairs with vidra-user's observability spec: the frontend
// sends it on every call and the backend accepts + echoes it, so a UI action and
// its backend request/log line share one id even with OpenTelemetry off. Use
// this exact header name on both sides (see .ralph/specs/observability.md).
const correlationHeader = "X-Correlation-ID"

// maxCorrelationIDLen bounds an inbound (untrusted) correlation id so a client
// cannot bloat log lines or response headers.
const maxCorrelationIDLen = 128

type correlationCtxKey struct{}

// correlationID accepts an inbound X-Correlation-ID, minting one from the
// server-generated request id when absent, sanitising it (it is untrusted client
// input), then echoes it on the response and stashes it on the request context
// so the request logger and downstream code can emit `correlation_id`.
//
// It must run after middleware.RequestID (so the request id exists to mint from)
// and before the request logger (so the value is present when the line is
// emitted). The value is never a secret and is safe to log/echo after sanitising.
func correlationID() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			cid := sanitizeCorrelationID(c.Request().Header.Get(correlationHeader))
			if cid == "" {
				// The Echo RequestID middleware has already set X-Request-Id on the
				// response; it is server-generated and safe to reuse.
				cid = c.Response().Header().Get(echo.HeaderXRequestID)
			}
			c.Response().Header().Set(correlationHeader, cid)
			ctx := context.WithValue(c.Request().Context(), correlationCtxKey{}, cid)
			c.SetRequest(c.Request().WithContext(ctx))
			return next(c)
		}
	}
}

// correlationIDFromContext returns the correlation id bound to ctx, or "".
func correlationIDFromContext(ctx context.Context) string {
	cid, _ := ctx.Value(correlationCtxKey{}).(string)
	return cid
}

// sanitizeCorrelationID keeps only URL-safe token characters and bounds the
// length, so an inbound header can never inject CR/LF into a response header or
// arbitrary content into a log line. Returns "" when nothing safe remains.
func sanitizeCorrelationID(s string) string {
	if len(s) > maxCorrelationIDLen {
		s = s[:maxCorrelationIDLen]
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		}
	}
	return b.String()
}
