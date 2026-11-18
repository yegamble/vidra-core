package httpapi

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

// federationOffRetryAfter is the Retry-After (seconds) on an ActivityPub request
// refused because federation is switched off: a day, since turning it back on is
// an operator decision, not a transient fault.
const federationOffRetryAfter = "86400"

// federationOffKey marks a request federationOff refused, so requestLogger logs
// its 503 at WARN (what the unmounted route's 404 got) instead of ERROR: it is a
// deliberate answer a migrated instance gives on every peer refresh, not a fault.
const federationOffKey = "federation_off"

// federationOff answers ActivityPub requests with 503 while federation is
// switched off (or not wired), instead of the 404 an unmounted route gives.
//
// The status is the whole point. PeerTube refreshes a remote actor or video by
// re-fetching its id, and on a 404 or 410 it DELETES its copy — the actor, its
// videos, and every follow on it — while any other failure is logged and the
// copy kept. An instance migrated from PeerTube that runs with federation off
// for a while would otherwise have its channels, videos and their followers
// erased from every peer that once federated with it. 503 says "not now".
//
// Only a request that is unmistakably ActivityPub is touched: a GET/HEAD of an
// actor, actor collection or object id whose Accept asks for activity+json or
// ld+json, and any POST to an inbox. Everything else — a browser, a REST client,
// webfinger and nodeinfo (which must not advertise a federating server) — falls
// through to exactly today's answer. It is a middleware rather than routes so
// the federation-off routing table, which the OpenAPI drift guard reads, is
// unchanged.
func (s *Server) federationOff() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if s.cfg.FederationEnabled && s.fedsvc != nil {
				return next(c)
			}
			r := c.Request()
			var refuse bool
			switch r.Method {
			case http.MethodPost:
				refuse = isInboxPath(r.URL.Path)
			case http.MethodGet, http.MethodHead:
				refuse = wantsActivityJSON(r.Header.Get("Accept")) && isActivityPubReadPath(r.URL.Path)
			}
			if !refuse {
				return next(c)
			}
			c.Set(federationOffKey, true)
			// no-store: the answer flips the moment the operator turns federation
			// on, and a cached 503 would keep refusing peers after that.
			c.Response().Header().Set("Cache-Control", "no-store")
			c.Response().Header().Set("Retry-After", federationOffRetryAfter)
			c.Response().Header().Add(echo.HeaderVary, echo.HeaderAccept)
			return c.JSON(http.StatusServiceUnavailable, ErrorResponse{Error: ErrorBody{
				Code:    "federation_disabled",
				Message: "federation is switched off on this instance",
			}})
		}
	}
}

// isActorKind matches the two actor path prefixes (Person, Group); the instance
// actor is served under /accounts/ as well.
func isActorKind(seg string) bool { return seg == "accounts" || seg == "video-channels" }

// isInboxPath matches POST /inbox and an actor's own /…/{handle}/inbox.
func isInboxPath(path string) bool {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	switch len(segs) {
	case 1:
		return segs[0] == "inbox"
	case 3:
		return isActorKind(segs[0]) && segs[1] != "" && segs[2] == "inbox"
	}
	return false
}

// isActivityPubReadPath matches the GET paths the federation block mounts: an
// actor, its outbox/followers/following, and the object ids (/videos/{id}, the
// legacy /videos/watch/{id}, /comments/{id}, /remote-comments/{id}). An object
// id is a UUID — the handlers 404 anything else even with federation on, so
// nothing else is an id a peer could hold.
func isActivityPubReadPath(path string) bool {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for _, s := range segs {
		if s == "" {
			return false
		}
	}
	switch len(segs) {
	case 2:
		switch segs[0] {
		case "accounts", "video-channels":
			return true
		case "videos", "comments", "remote-comments":
			return isUUID(segs[1])
		}
	case 3:
		if isActorKind(segs[0]) {
			switch segs[2] {
			case "outbox", "followers", "following":
				return true
			}
		}
		return segs[0] == "videos" && segs[1] == "watch" && isUUID(segs[2])
	}
	return false
}

func isUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}
