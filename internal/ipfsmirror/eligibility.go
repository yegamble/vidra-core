package ipfsmirror

// Visibility model constants (mirror the DB CHECK constraints).
const (
	privacyPublic  = "public"    // videos.privacy, playlists.visibility
	statePublished = "published" // videos.state
)

// Network is the swarm a subject routes to (mirrors media_ipfs_pins.network). The
// empty string means "not mirrored on any network" (the default-deny outcome).
const (
	NetworkPublic  = "public"
	NetworkPrivate = "private"
	NetworkNone    = "" // not mirrored anywhere
)

// Subject is everything the eligibility gate needs to know about one media
// object to decide which swarm it routes to (public, private, or none). It
// carries only visibility facts — the caller resolves them from committed DB
// state (the enqueue helpers do the lookups). The zero value is "nothing is
// known", which the gate treats as NOT mirrored anywhere (default-deny).
type Subject struct {
	// Class is the media class. An unrecognized or never-mirror class is refused.
	Class MediaClass

	// Video-derived classes (video_original, hls, webm, thumbnail, storyboard,
	// storyboard_vtt, caption): the parent video's privacy and state. Both must be
	// public+published — unlisted, private, quarantined, draft, processing,
	// scheduled and failed all fail the gate. In ADDITION, the owning account must
	// not be unlisted (OwnerUnlisted below): unlisted is treated as PRIVATE for
	// mirroring (spec §7), so an unlisted owner's public videos are never pinned.
	VideoPrivacy string
	VideoState   string

	// OwnerActive / OwnerUnlisted are the owning account's flags. For identity
	// images (user/channel avatar+banner) BOTH gate: a non-active
	// (deactivated/deleted) or unlisted account's images are not mirrored — a
	// permanent public CID would defeat the unlistedness promise. For video-derived
	// classes ONLY OwnerUnlisted gates (in addition to video privacy+state): a
	// permanent public CID for an unlisted owner's video would defeat the
	// URL-unguessability that "unlisted" promises (spec §3/§7). Account-active is
	// not gated for video-derived classes here — deactivation/deletion is a separate
	// re-evaluation trigger (§3) handled by the account delete/unpin path.
	OwnerActive   bool
	OwnerUnlisted bool

	// Playlist cover: the playlist's visibility. Only public playlists' covers.
	PlaylistVisibility string
}

// Route is THE privacy fence (eligibility matrix v2, spec §4): it maps a subject to
// the swarm it may be mirrored on — NetworkPublic, NetworkPrivate, or NetworkNone
// (not mirrored anywhere). It is a pure function of committed visibility facts,
// DEFAULT-DENY: any class that is not an explicitly-handled, mirror-eligible class
// returns NetworkNone. Every never-mirror class (dm_attachment — plaintext AND the
// future e2ee-blobs, which do not exist yet; account_export, upload_chunk, live_edge,
// remote_thumbnail) and any unknown class falls through to the default and is refused
// on BOTH swarms. This is the one place the routing policy lives; the enqueue helpers
// call it (via the tier-gating effectiveNetwork wrapper) before writing any ledger
// row, and the eligibility unit table exercises every class × visibility × network.
//
// v1 routing (user-resolved 2026-07-07):
//   - public+published video (+ every derivative), owner listed → public swarm
//   - private OR unlisted video (+ derivatives), OR a public video whose OWNER is
//     unlisted → private swarm (unlisted is treated as private for mirroring: a
//     permanent public CID would defeat URL-unguessability, spec §7)
//   - quarantined / draft / processing / scheduled / failed video → NONE (unvetted or
//     not-yet-live content stays out of EVERY mirror until it is published)
//   - avatar/banner of an active+listed account → public; of an unlisted OR
//     deactivated account → private
//   - playlist cover: public playlist → public; non-public playlist → private
//   - DM attachments, account exports, upload chunks, live edge, remote thumbnails,
//     anything unknown → NONE on either swarm
//
// Route decides the ELIGIBLE swarm from visibility alone; whether that swarm is
// actually mirrored on this instance (the private tier may be off — the default) is
// the caller's tier-gate in Service.effectiveNetwork.
func Route(s Subject) string {
	switch {
	case isVideoDerived(s.Class):
		// Only a PUBLISHED video is mirrored at all; quarantined/draft/processing/
		// scheduled/failed route nowhere (picked up on the eventual publish transition).
		if s.VideoState != statePublished {
			return NetworkNone
		}
		if s.VideoPrivacy == privacyPublic && !s.OwnerUnlisted {
			return NetworkPublic
		}
		// private, unlisted, or public-with-unlisted-owner → the private swarm.
		return NetworkPrivate
	case isIdentityImage(s.Class):
		if s.OwnerActive && !s.OwnerUnlisted {
			return NetworkPublic
		}
		// Unlisted or deactivated account identity assets → the private swarm.
		return NetworkPrivate
	case s.Class == ClassPlaylistCover:
		if s.PlaylistVisibility == privacyPublic {
			return NetworkPublic
		}
		return NetworkPrivate
	default:
		// Never-mirror classes and anything unrecognized: refused on both swarms.
		return NetworkNone
	}
}
