package ipfsmirror

// Visibility model constants (mirror the DB CHECK constraints).
const (
	privacyPublic  = "public"    // videos.privacy, playlists.visibility
	statePublished = "published" // videos.state
)

// Subject is everything the eligibility gate needs to know about one media
// object to decide whether it may be mirrored to the PUBLIC IPFS network. It
// carries only visibility facts — the caller resolves them from committed DB
// state (the enqueue helpers do the lookups). The zero value is "nothing is
// known", which the gate treats as NOT eligible (default-deny).
type Subject struct {
	// Class is the media class. An unrecognized or never-mirror class is refused.
	Class MediaClass

	// Video-derived classes (video_original, hls, webm, thumbnail, storyboard,
	// storyboard_vtt, caption): the parent video's privacy and state. Both must be
	// public+published — unlisted, private, quarantined, draft, processing,
	// scheduled and failed all fail the gate.
	VideoPrivacy string
	VideoState   string

	// Identity images (user/channel avatar+banner): the owning account's flags. A
	// non-active (deactivated/deleted) or unlisted account's images are not
	// mirrored — a permanent public CID would defeat the unlistedness promise.
	OwnerActive   bool
	OwnerUnlisted bool

	// Playlist cover: the playlist's visibility. Only public playlists' covers.
	PlaylistVisibility string
}

// Eligible is THE privacy fence: it reports whether the subject may be added and
// pinned to the public IPFS network in v1. It is a pure function of committed
// visibility facts, DEFAULT-DENY: any class that is not an explicitly-handled,
// mirror-eligible public class returns false. Every never-mirror class
// (dm_attachment, account_export, upload_chunk, live_edge, remote_thumbnail) and
// any unknown class falls through to the default and is refused. This is the one
// place the public-only v1 policy is enforced; the enqueue helpers call it before
// writing any ledger row, and the eligibility unit table exercises every
// class × visibility combination against it.
func Eligible(s Subject) bool {
	switch {
	case isVideoDerived(s.Class):
		return s.VideoPrivacy == privacyPublic && s.VideoState == statePublished
	case isIdentityImage(s.Class):
		return s.OwnerActive && !s.OwnerUnlisted
	case s.Class == ClassPlaylistCover:
		return s.PlaylistVisibility == privacyPublic
	default:
		// Never-mirror classes and anything unrecognized: refused, always.
		return false
	}
}
