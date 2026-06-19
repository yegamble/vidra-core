package httpapi

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/ratelimit"
)

// rateLimit returns middleware that enforces the limiter per client IP and sets
// standard X-RateLimit-* headers. On the limiter's backing store failing (e.g.
// Redis unreachable) it FAILS OPEN — the request is allowed and the error is
// logged — so a Redis blip degrades protection rather than availability. Denied
// requests get a 429 rate_limited envelope with Retry-After.
func (s *Server) rateLimit(limiter *ratelimit.Limiter) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			key := "ip:" + c.RealIP()
			res, err := limiter.Allow(c.Request().Context(), key)
			if err != nil {
				s.logger.Warn("rate limiter unavailable, failing open",
					"error", err,
					"path", c.Path(),
					"request_id", c.Response().Header().Get(echo.HeaderXRequestID),
				)
				return next(c)
			}

			h := c.Response().Header()
			h.Set("X-RateLimit-Limit", strconv.Itoa(res.Limit))
			h.Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
			h.Set("X-RateLimit-Reset", strconv.Itoa(int(res.Reset.Seconds())))

			if !res.Allowed {
				retry := int(res.RetryAfter.Seconds())
				if retry < 1 {
					retry = 1
				}
				h.Set("Retry-After", strconv.Itoa(retry))
				return echo.NewHTTPError(http.StatusTooManyRequests, "rate limit exceeded")
			}
			return next(c)
		}
	}
}
