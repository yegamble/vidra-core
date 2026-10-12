package httpapi

import (
	"net/http"
	"net/url"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/feedtoken"
	"github.com/vidra/vidra-core/internal/ratelimit"
)

// Private subscriptions feed: GET /feeds/subscriptions.xml?token=… renders the
// caller's subscriptions as MRSS for a feed reader that cannot send a bearer
// header. The token (see internal/feedtoken) authorises THIS feed only, is
// stored hashed, and is shown to its owner once at rotation. Any unknown,
// revoked or malformed token is the same 404, and the route has its own
// per-IP budget so the token space cannot be probed at line rate.

// WithFeedTokenService wires the private-feed token store. When unset, the
// token endpoints and the feed are not registered.
func WithFeedTokenService(svc *feedtoken.Service) Option {
	return func(s *Server) { s.feedtokensvc = svc }
}

type feedTokenStatus struct {
	Configured bool       `json:"configured"`
	CreatedAt  *time.Time `json:"created_at,omitempty"`
}

type feedTokenIssued struct {
	Token string `json:"token"`
	// URL is the ready-to-paste feed address; empty when the instance has no
	// PUBLIC_BASE_URL to build it from.
	URL       string    `json:"url,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Server) handleGetFeedToken(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	at, ok, err := s.feedtokensvc.CreatedAt(c.Request().Context(), userID)
	if err != nil {
		return err
	}
	if !ok {
		return c.JSON(http.StatusOK, feedTokenStatus{})
	}
	return c.JSON(http.StatusOK, feedTokenStatus{Configured: true, CreatedAt: &at})
}

func (s *Server) handleRotateFeedToken(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	raw, err := s.feedtokensvc.Rotate(c.Request().Context(), userID)
	if err != nil {
		return err
	}
	out := feedTokenIssued{Token: raw, CreatedAt: time.Now().UTC()}
	if s.webOrigin() != "" {
		out.URL = s.webOrigin() + "/feeds/subscriptions.xml?token=" + url.QueryEscape(raw)
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusCreated, out)
}

func (s *Server) handleRevokeFeedToken(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if err := s.feedtokensvc.Revoke(c.Request().Context(), userID); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// feedRateLimit gives the token-gated feed its own per-IP budget, riding the
// general limiter (the root mount is otherwise unthrottled) and failing open
// like every limiter here.
func (s *Server) feedRateLimit() echo.MiddlewareFunc {
	return s.limitBy(limitRule{
		resolve: func(c echo.Context) (*ratelimit.Limiter, string, bool) {
			if s.limiter == nil {
				return nil, "", false
			}
			return s.limiter, "feedtoken:" + c.RealIP(), true
		},
		unavailable: "feed rate limiter unavailable, failing open",
		denied:      "rate limit exceeded",
	})
}

func (s *Server) handleSubscriptionsFeed(c echo.Context) error {
	notFound := echo.NewHTTPError(http.StatusNotFound, "feed not found")
	ctx := c.Request().Context()
	userID, ok, err := s.feedtokensvc.Owner(ctx, c.QueryParam("token"))
	if err != nil {
		return err
	}
	if !ok {
		return notFound
	}
	items, _, err := s.videosvc.ListSubscriptions(ctx, userID, feedItemLimit, 0)
	if err != nil {
		return err
	}
	origin, instance := s.webOrigin(), s.instanceName()
	doc := rssDocument{
		Version: "2.0", MediaNS: mediaRSSNamespace, AtomNS: atomNamespace,
		Channel: rssChannel{
			Title:       "Subscriptions — " + instance,
			Link:        origin + "/subscriptions",
			Description: "New videos from the channels you follow on " + instance,
			// No self link: it would embed the secret in the document body.
		},
	}
	hide := s.hideSensitiveVideos()
	for _, it := range items {
		// Local public videos only: remote cards have no local watch page, and
		// the public-only rule is the same one the anonymous feed holds.
		if it.Remote || it.Video.Privacy != "public" || it.Video.State != "published" || (hide && it.Video.IsSensitive) {
			continue
		}
		doc.Channel.Items = append(doc.Channel.Items, s.rssItemFor(it))
	}
	if len(doc.Channel.Items) > 0 {
		doc.Channel.LastBuildDate = doc.Channel.Items[0].PubDate
	}
	// The URL is a bearer secret: never cache it in a shared cache, and keep it
	// out of Referer if a reader follows an item link.
	c.Response().Header().Set("Referrer-Policy", "no-referrer")
	return writeXML(c, contentTypeRSS, "private, no-store", doc)
}
