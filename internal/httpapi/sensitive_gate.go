package httpapi

import (
	"github.com/labstack/echo/v5"

	"github.com/vidra/vidra-core/internal/instancesettings"
)

// The sign-in wall for sensitive videos (sensitive_anonymous_access).
//
// Off by default. When an operator sets it to "sign_in", a caller without an
// account is refused every read of a video flagged sensitive: videoReadBase
// answers 401 sign_in_required, which covers the detail document and every
// media route, and videoEdgeEligible stops those bytes being handed to
// anything a shared cache could keep, so the CDN's own anonymous origin fetch
// meets the same 401. It proves an account, not an age.

// sensitiveAnonymousAccess is the setting's current value (open | sign_in).
func (s *Server) sensitiveAnonymousAccess() string {
	return s.settingString(instancesettings.KeySensitiveAnonymousAccess, instancesettings.SensitiveAnonymousAccessOpen)
}

// sensitiveGated reports whether a video with this sensitive flag is behind
// the sign-in wall right now.
func (s *Server) sensitiveGated(isSensitive bool) bool {
	return isSensitive && s.sensitiveAnonymousAccess() == instancesettings.SensitiveAnonymousAccessSignIn
}

// videoEdgeEligible is the fence for delivering a video's assets through
// anything off-origin or shared (a CDN, a presigned redirect, a public cache
// policy): public AND published, as publicVideoForIPFS has always required,
// AND not behind the sign-in wall. A gated video is served by the API byte
// path alone, to callers it has authenticated.
//
// The purge snapshot (media_purge.go) deliberately keeps the older, wider
// fence: copies cached while a video was still eligible are exactly what has
// to be found and evicted after it stops being so.
func (s *Server) videoEdgeEligible(privacy, state string, isSensitive bool) bool {
	return publicVideoForIPFS(privacy, state) && !s.sensitiveGated(isSensitive)
}

// hideSensitiveOnOpenSurfaces is the hide flag for the surfaces that have no
// viewer at all and are read by machines (the RSS and podcast feeds, the
// sitemap): the "hide" policy as before, and also the sign-in wall — a feed
// reader or a crawler can never sign in, so advertising a gated video to one
// only hands out a link that answers 401.
func (s *Server) hideSensitiveOnOpenSurfaces() bool {
	return s.hideSensitiveVideos() || s.sensitiveGated(true)
}

// gateCards marks the sensitive cards in a list for a caller without an
// account while the wall is up: sign_in_required tells the client to show the
// sign-in prompt instead of a player, and has_thumbnail=false because the
// poster route would answer 401. Whether a sensitive video is LISTED at all is
// still sensitive_content_policy's decision; this only stops a list promising
// what the caller cannot open.
func (s *Server) gateCards(anonymous bool, views []videoView) {
	if !anonymous || !s.sensitiveGated(true) {
		return
	}
	no := false
	for i := range views {
		if views[i].IsSensitive && !views[i].Remote {
			views[i].SignInRequired = true
			views[i].HasThumbnail = &no
		}
	}
}

// anonymousCaller reports whether the request carries no session.
func anonymousCaller(c *echo.Context) bool {
	_, _, ok := principalFromContext(c)
	return !ok
}

// sensitiveGateSwitchRefusal is why a settings change that puts the wall up
// must be refused, or "". With CDN delivery on, the edge is already holding
// sensitive videos' bytes under public cache lifetimes; the wall stops new
// copies but cannot recall those, so without a purge path it would report a
// protection it is not giving.
func (s *Server) sensitiveGateSwitchRefusal(updates map[string]instancesettings.Update) string {
	if !s.raisesSensitiveGate(updates) {
		return ""
	}
	cdnOn := s.cdnDeliveryEnabled()
	if u, ok := updates[instancesettings.KeyDeliveryCDNEnabled]; ok {
		cdnOn = !u.Delete && u.Value == "true"
	}
	if s.mediaCDNEdge != nil && cdnOn && !s.cdnCanPurge() {
		return "cannot be set to sign_in while CDN delivery is on and no CDN purge endpoint is configured: copies of sensitive videos already cached at the edge would stay public. Configure the purge endpoint, or turn delivery_cdn_enabled off and wait out the edge's cache lifetime, then try again"
	}
	return ""
}

// raisesSensitiveGate reports whether this batch moves the setting from open
// to sign_in.
func (s *Server) raisesSensitiveGate(updates map[string]instancesettings.Update) bool {
	u, ok := updates[instancesettings.KeySensitiveAnonymousAccess]
	return ok && !u.Delete && u.Value == instancesettings.SensitiveAnonymousAccessSignIn &&
		s.sensitiveAnonymousAccess() != instancesettings.SensitiveAnonymousAccessSignIn
}

// sensitiveGateRaisedNotice is what the operator has to do themselves after
// the wall goes up on an instance with a CDN in front of it.
const sensitiveGateRaisedNotice = "sensitive_anonymous_access is now sign_in. Purge the CDN zone: copies of sensitive videos the CDN cached before this change stay publicly reachable until they are purged or expire."

// cdnCanPurge reports whether the wired CDN has an invalidation path.
func (s *Server) cdnCanPurge() bool { return s.cdnPurgeAvailable != nil && s.cdnPurgeAvailable() }
