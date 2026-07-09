package httpapi

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/observability"
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

// attachmentUploadRateLimit throttles DM attachment uploads PER USER (not per
// IP). DM attachments do not count against the storage quota (messaging-v2.md
// D6), so this is the compensating anti-abuse control. It is a no-op passthrough
// when no attachment limiter is configured (e.g. unit tests) or the request did
// not carry an authenticated principal (requireAuth runs first, so that is only a
// defensive fallback). Like the other limiters it FAILS OPEN if the store is down
// and denies with a 429 rate_limited envelope plus Retry-After.
func (s *Server) attachmentUploadRateLimit() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if s.attachmentLimit == nil {
				return next(c)
			}
			userID, _, ok := principalFromContext(c)
			if !ok {
				return next(c)
			}
			res, err := s.attachmentLimit.Allow(c.Request().Context(), "dm-attach:"+userID.String())
			if err != nil {
				s.logger.Warn("attachment rate limiter unavailable, failing open",
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
				return echo.NewHTTPError(http.StatusTooManyRequests, "attachment upload rate limit exceeded")
			}
			return next(c)
		}
	}
}

// contactRateLimit throttles the public contact form (POST /instance/contact)
// with its own dedicated budget — 1 request per IP per hour in production
// (wired in cmd/api) — keyed independently ("contact:" + client IP) so the
// general API budget is untouched. It follows the authRateLimit pattern: an
// audit event on denial (never the message or any address), 429 + Retry-After,
// and FAIL OPEN when the backing store is down.
func (s *Server) contactRateLimit(limiter *ratelimit.Limiter) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			key := "contact:" + c.RealIP()
			res, err := limiter.Allow(c.Request().Context(), key)
			if err != nil {
				s.logger.Warn("contact rate limiter unavailable, failing open",
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
				s.audit(c, observability.ActionRateLimited, observability.ResultFailure, "", "contact_rate_limited")
				return echo.NewHTTPError(http.StatusTooManyRequests, "rate limit exceeded")
			}
			return next(c)
		}
	}
}

// authRateLimit is a stricter limiter for the sensitive auth endpoints. It keys
// independently of the general limiter ("auth:" + client IP) so credential
// stuffing on login/register/confirm endpoints is throttled without touching the
// general API budget. On denial it records an audit event (never the credentials)
// and returns 429. Like the general limiter it FAILS OPEN if the store is down.
func (s *Server) authRateLimit(limiter *ratelimit.Limiter) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			key := "auth:" + c.RealIP()
			res, err := limiter.Allow(c.Request().Context(), key)
			if err != nil {
				s.logger.Warn("auth rate limiter unavailable, failing open",
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
				s.audit(c, observability.ActionRateLimited, observability.ResultFailure, "", "auth_rate_limited")
				return echo.NewHTTPError(http.StatusTooManyRequests, "rate limit exceeded")
			}
			return next(c)
		}
	}
}
