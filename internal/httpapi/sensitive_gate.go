package httpapi

import "github.com/vidra/vidra-core/internal/instancesettings"

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
