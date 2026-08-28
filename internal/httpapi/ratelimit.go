package httpapi

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/ratelimit"
)

// limitRule is one budget's policy: everything the five rate-limit middlewares
// below actually differ by. The mechanics they share — fail open, the
// X-RateLimit-* headers, the Retry-After floor, the 429 envelope — live once, in
// limitBy.
type limitRule struct {
	// resolve picks the limiter and the key to charge for this request.
	// Returning ok=false passes the request through UNLIMITED: either no limiter
	// was wired (an embedder that never configured this budget) or the request
	// carries no principal for a per-user budget. Both are deliberate
	// passthroughs, not denials.
	resolve func(c echo.Context) (limiter *ratelimit.Limiter, key string, ok bool)
	// unavailable is the warning logged when the backing store errors and the
	// request is let through. One line per budget so the log says which one.
	unavailable string
	// denied is the client-visible message on the 429.
	denied string
	// auditReason, when non-empty, records an ActionRateLimited audit event on
	// denial carrying it. Only the budgets guarding credentials, mail and
	// the public contact form audit; the general and attachment budgets are
	// ordinary throttles and would only add noise.
	auditReason string
	// auditActor supplies the actor id for that event ("" for IP-keyed budgets,
	// where there is no authenticated principal to name).
	auditActor func(c echo.Context) string
}

// limitBy enforces one budget. It FAILS OPEN on the limiter's backing store
// failing (e.g. Redis unreachable) — the request is allowed and the error is
// logged — so a Redis blip degrades protection rather than availability. Denied
// requests get a 429 rate_limited envelope with Retry-After.
//
// Retry-After is floored at 1: a sub-second window would otherwise emit
// "Retry-After: 0", which clients read as "retry immediately".
func (s *Server) limitBy(rule limitRule) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			limiter, key, ok := rule.resolve(c)
			if !ok {
				return next(c)
			}
			res, err := limiter.Allow(c.Request().Context(), key)
			if err != nil {
				s.logger.Warn(rule.unavailable,
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
				if rule.auditReason != "" {
					actor := ""
					if rule.auditActor != nil {
						actor = rule.auditActor(c)
					}
					s.audit(c, observability.ActionRateLimited, observability.ResultFailure, actor, rule.auditReason)
				}
				return echo.NewHTTPError(http.StatusTooManyRequests, rule.denied)
			}
			return next(c)
		}
	}
}

// rateLimit returns middleware that enforces the limiter per client IP and sets
// standard X-RateLimit-* headers.
//
// It carries TWO budgets on one mount. Media GETs (mediaRouteTemplates) are
// cheap, cacheable blob reads that a single page view fans out ~20 of — plus
// ~10 HLS segments a minute during playback — so charging them to the general
// API budget 429s an ordinary viewer inside their first minute. They are keyed
// separately ("media:" vs "ip:") against s.mediaLimit, so a segment burst can
// never spend the budget the same caller needs for real API calls, and the two
// ceilings can be tuned independently (RATE_LIMIT_REQUESTS /
// MEDIA_RATE_LIMIT_REQUESTS). s.mediaLimit falls back to the general limiter
// when none was wired, so an embedder that only sets WithRateLimiter keeps the
// old behaviour apart from the key prefix.
//
// The client IP itself comes from the Echo IPExtractor installed in New(), which
// only honours X-Forwarded-For from loopback/private hops — see the comment
// there. Without that, this and every other per-IP budget is header-spoofable.
func (s *Server) rateLimit(limiter *ratelimit.Limiter) echo.MiddlewareFunc {
	return s.limitBy(limitRule{
		resolve: func(c echo.Context) (*ratelimit.Limiter, string, bool) {
			// Locals, never the captured parameter: reassigning `limiter` here
			// would permanently swap the general limiter for the media one on the
			// first media request.
			active, key := limiter, "ip:"+c.RealIP()
			if isMediaRoute(c) && s.mediaLimit != nil {
				active, key = s.mediaLimit, "media:"+c.RealIP()
			}
			return active, key, true
		},
		unavailable: "rate limiter unavailable, failing open",
		denied:      "rate limit exceeded",
	})
}

// attachmentUploadRateLimit throttles DM attachment uploads PER USER (not per
// IP). DM attachments do not count against the storage quota (messaging-v2.md
// D6), so this is the compensating anti-abuse control. It is a no-op passthrough
// when no attachment limiter is configured (e.g. unit tests) or the request did
// not carry an authenticated principal (requireAuth runs first, so that is only a
// defensive fallback).
func (s *Server) attachmentUploadRateLimit() echo.MiddlewareFunc {
	return s.limitBy(limitRule{
		resolve: func(c echo.Context) (*ratelimit.Limiter, string, bool) {
			if s.attachmentLimit == nil {
				return nil, "", false
			}
			userID, _, ok := principalFromContext(c)
			if !ok {
				return nil, "", false
			}
			return s.attachmentLimit, "dm-attach:" + userID.String(), true
		},
		unavailable: "attachment rate limiter unavailable, failing open",
		denied:      "attachment upload rate limit exceeded",
	})
}

// contactRateLimit throttles the public contact form (POST /instance/contact)
// with its own dedicated budget — 1 request per IP per hour in production
// (wired in cmd/api) — keyed independently ("contact:" + client IP) so the
// general API budget is untouched. It follows the authRateLimit pattern: an
// audit event on denial (never the message or any address), 429 + Retry-After,
// and FAIL OPEN when the backing store is down.
func (s *Server) contactRateLimit(limiter *ratelimit.Limiter) echo.MiddlewareFunc {
	return s.limitBy(limitRule{
		resolve: func(c echo.Context) (*ratelimit.Limiter, string, bool) {
			return limiter, "contact:" + c.RealIP(), true
		},
		unavailable: "contact rate limiter unavailable, failing open",
		denied:      "rate limit exceeded",
		auditReason: "contact_rate_limited",
	})
}

// mailTestRateLimit throttles the admin mail probe (POST /admin/mail/test) PER
// ADMIN USER, on its own budget — 3 per hour in production wiring. The endpoint
// always mails the instance's own contact address, so this is not an anti-relay
// control; it exists because a browser tab stuck in a retry loop is enough to
// get a domain throttled or blacklisted by its own relay, and that failure
// arrives days later as "password resets stopped working".
//
// It follows the contact limiter exactly: a no-op passthrough when no limiter
// is wired, an audit event on denial (never an address), 429 + Retry-After, and
// FAIL OPEN when the backing store is down. Keyed by user id, not IP: two
// admins behind one office address should not fight over one budget.
func (s *Server) mailTestRateLimit() echo.MiddlewareFunc {
	return s.limitBy(limitRule{
		resolve: func(c echo.Context) (*ratelimit.Limiter, string, bool) {
			if s.mailTestLimit == nil {
				return nil, "", false
			}
			userID, _, ok := principalFromContext(c)
			if !ok {
				// requireAuth runs first, so this is only a defensive fallback.
				return nil, "", false
			}
			return s.mailTestLimit, "mail-test:" + userID.String(), true
		},
		unavailable: "mail test rate limiter unavailable, failing open",
		denied:      "you have sent several test messages recently; wait before sending another",
		auditReason: "mail_test_rate_limited",
		auditActor: func(c echo.Context) string {
			userID, _, _ := principalFromContext(c)
			return userID.String()
		},
	})
}

// authRateLimit is a stricter limiter for the sensitive auth endpoints. It keys
// independently of the general limiter ("auth:" + client IP) so credential
// stuffing on login/register/confirm endpoints is throttled without touching the
// general API budget. On denial it records an audit event (never the credentials)
// and returns 429. Like the general limiter it FAILS OPEN if the store is down.
func (s *Server) authRateLimit(limiter *ratelimit.Limiter) echo.MiddlewareFunc {
	return s.limitBy(limitRule{
		resolve: func(c echo.Context) (*ratelimit.Limiter, string, bool) {
			return limiter, "auth:" + c.RealIP(), true
		},
		unavailable: "auth rate limiter unavailable, failing open",
		denied:      "rate limit exceeded",
		auditReason: "auth_rate_limited",
	})
}
