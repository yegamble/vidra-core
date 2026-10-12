package video

// PrivacyInternal is PeerTube's "internal" tier (its VideoPrivacy 4): the video
// is visible to any signed-in, active account of THIS instance and to nobody
// anonymous. It is never federated, indexed for public search, put in an RSS
// feed or sitemap, offered over oEmbed, cross-posted, or mirrored on the public
// IPFS swarm — every one of those paths allowlists privacy='public', so the tier
// is excluded there by construction. This file is the read-side rule.
const PrivacyInternal = "internal"

// PrivacyAdmits reports whether a video's privacy tier ALONE lets this viewer
// read it; state, moderation and the password unlock are separate gates layered
// on top by the caller. authed means a signed-in, active local account (the auth
// middleware rejects revoked, deactivated and deleted principals before one is
// ever set).
//
// It is fail-closed on purpose: a value this build does not know about admits
// the owner only, so a tier added later is never treated as a wider one.
func PrivacyAdmits(privacy string, authed, isOwner bool) bool {
	switch privacy {
	case "public", "unlisted", PrivacyPassword:
		return true
	case PrivacyInternal:
		return authed
	default: // "private", and anything unknown
		return authed && isOwner
	}
}
