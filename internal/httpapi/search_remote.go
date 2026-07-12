package httpapi

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/instancesettings"
)

// Remote-URI search (config-parity W13, gap-matrix §10). A URI- or
// handle-shaped search query ("https://…", "@user@host", "user@host") is
// resolved to remote content through the EXISTING federation resolution
// machinery (WebFinger → actor cache → SSRF-guarded fetches; see
// federation.ResolveSearchTarget), gated per auth state:
//
//   - search_remote_uri_users     (default true)  — logged-in callers
//   - search_remote_uri_anonymous (default false) — anonymous callers (the
//     SSRF/abuse surface; PeerTube defaults this off too)
//
// LATENCY CONTRACT (documented decision): resolution runs CONCURRENTLY with
// the local search under a strict deadline (remoteSearchResolveTimeout), so a
// slow or dead origin delays the response by at most that deadline and normal
// (non-shaped) searches are entirely unaffected. Any failure — bad gate, rate
// limit exhausted, unresolvable target, timeout — degrades silently to
// local-only results; remote resolution can never fail a search.

const (
	// remoteSearchResolveTimeout bounds one search's remote resolution
	// (WebFinger + actor/object fetch + store) — tight, per the wave spec's
	// 2-3s so search latency stays acceptable when an origin is slow.
	remoteSearchResolveTimeout = 2500 * time.Millisecond
	// defaultSearchResolveRateLimit / defaultSearchResolveRateWindow are the
	// built-in per-caller resolution budget (see Server.searchResolveLimit).
	defaultSearchResolveRateLimit  = 10
	defaultSearchResolveRateWindow = time.Minute
)

// remoteSearchActorView is a resolved remote channel/account search hit: the
// followable identity (POST /me/remote-follows accepts either the handle or
// the actor URL).
type remoteSearchActorView struct {
	ActorURL string `json:"actor_url"`
	Handle   string `json:"handle"` // name@domain
	Domain   string `json:"domain"`
}

// remoteSearchResultView is one typed remote search hit riding the search
// response's `remote` array: type video carries a RemoteVideo card (same
// projection as GET /remote-videos/{id}); type channel/account carries the
// resolved actor identity.
type remoteSearchResultView struct {
	Type  string                 `json:"type"` // video | channel | account
	Video *remoteVideoView       `json:"video,omitempty"`
	Actor *remoteSearchActorView `json:"actor,omitempty"`
}

// --- gates (instance-settings overlay; see admin_instance_settings.go) ---

// remoteSearchAvailable reports the boot capability: federation is on and the
// federation service is wired. Without it both gates are moot (and the
// /instance search block reports false).
func (s *Server) remoteSearchAvailable() bool {
	return s.cfg.FederationEnabled && s.fedsvc != nil
}

// searchRemoteURIUsers / searchRemoteURIAnonymous are the runtime admin
// settings (config-parity W13). Defaults: users on (PT parity), anonymous off
// (SSRF/abuse surface).
func (s *Server) searchRemoteURIUsers() bool {
	return s.settingBool(instancesettings.KeySearchRemoteURIUsers, true)
}

func (s *Server) searchRemoteURIAnonymous() bool {
	return s.settingBool(instancesettings.KeySearchRemoteURIAnonymous, false)
}

// remoteSearchAllowed resolves the EFFECTIVE gate for one caller's auth state:
// the boot capability AND the matching runtime setting.
func (s *Server) remoteSearchAllowed(authed bool) bool {
	if !s.remoteSearchAvailable() {
		return false
	}
	if authed {
		return s.searchRemoteURIUsers()
	}
	return s.searchRemoteURIAnonymous()
}

// startRemoteSearch kicks off remote resolution for one search request when it
// applies: the query is URI/handle-shaped, the caller's auth-state gate is on,
// and the caller's resolution budget (searchResolveLimit) is not exhausted.
// It returns nil when resolution does not apply; otherwise a 1-buffered
// channel that ALWAYS receives exactly one value (possibly nil) within
// remoteSearchResolveTimeout — the handler runs the local search concurrently
// and joins on it.
func (s *Server) startRemoteSearch(c echo.Context, q string) <-chan []remoteSearchResultView {
	userID, _, authed := principalFromContext(c)
	if !s.remoteSearchAllowed(authed) {
		return nil
	}
	if federation.ClassifySearchQuery(q) == federation.SearchQueryNone {
		return nil
	}
	// Per-caller budget: the user id when authed, else the client IP. A
	// counter error fails CLOSED (skip resolution) — outbound fetches never
	// run unbudgeted — and an exhausted budget degrades to local-only.
	key := "search-resolve:ip:" + c.RealIP()
	if authed {
		key = "search-resolve:user:" + userID.String()
	}
	res, err := s.searchResolveLimit.Allow(c.Request().Context(), key)
	if err != nil || !res.Allowed {
		return nil
	}
	ch := make(chan []remoteSearchResultView, 1)
	ctx, cancel := context.WithTimeout(c.Request().Context(), remoteSearchResolveTimeout)
	go func() {
		defer cancel()
		ch <- s.resolveRemoteSearch(ctx, q)
	}()
	return ch
}

// resolveRemoteSearch resolves one shaped query and shapes the typed result
// items. Every failure returns nil (silent local-only degradation).
func (s *Server) resolveRemoteSearch(ctx context.Context, q string) []remoteSearchResultView {
	resolved, err := s.fedsvc.ResolveSearchTarget(ctx, q)
	if err != nil {
		return nil
	}
	switch {
	case resolved.Actor != nil:
		return []remoteSearchResultView{{
			Type: resolved.Actor.Kind,
			Actor: &remoteSearchActorView{
				ActorURL: resolved.Actor.ActorURL,
				Handle:   resolved.Actor.Handle,
				Domain:   resolved.Actor.Domain,
			},
		}}
	case resolved.VideoID != uuid.Nil:
		if s.remotevideosvc == nil {
			return nil
		}
		// Re-read through the remote-video read model: it applies the admin
		// instance blocklist and the per-video hide, so a blocked origin's
		// video never surfaces even right after resolution.
		rv, err := s.remotevideosvc.Get(ctx, resolved.VideoID)
		if err != nil {
			return nil
		}
		view := newRemoteVideoView(rv)
		return []remoteSearchResultView{{Type: federation.SearchResultVideo, Video: &view}}
	}
	return nil
}
