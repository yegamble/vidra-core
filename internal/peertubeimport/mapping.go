package peertubeimport

import "strconv"

// This file is the pure PeerTube→Vidra value mapping: enum translations and
// small helpers with no I/O, so they are exhaustively unit-testable.

// mapRole maps a PeerTube UserRole (0 ADMINISTRATOR, 1 MODERATOR, 2 USER) to a
// Vidra role. Unknown values fall back to the least-privileged "user".
func mapRole(ptRole int) string {
	switch ptRole {
	case 0:
		return "admin"
	case 1:
		return "moderator"
	default:
		return "user"
	}
}

// mapPrivacy maps a PeerTube VideoPrivacy (1 PUBLIC, 2 UNLISTED, 3 PRIVATE,
// 4 INTERNAL) to a Vidra privacy. INTERNAL has no Vidra equivalent and maps to
// the safest option, private. Unknown values default to private.
func mapPrivacy(ptPrivacy int) string {
	switch ptPrivacy {
	case 1:
		return "public"
	case 2:
		return "unlisted"
	default: // 3 PRIVATE, 4 INTERNAL, anything else
		return "private"
	}
}

// mapPlaylistPrivacy maps a PeerTube VideoPlaylistPrivacy (1 PUBLIC, 2 UNLISTED,
// 3 PRIVATE) to a Vidra playlist visibility.
func mapPlaylistPrivacy(ptPrivacy int) string {
	switch ptPrivacy {
	case 1:
		return "public"
	case 2:
		return "unlisted"
	default:
		return "private"
	}
}

// mapVideoState maps a PeerTube VideoState to a Vidra state. Only PeerTube's
// PUBLISHED (1) becomes 'published'; every other state (to-transcode, to-import,
// waiting-for-live, failed, …) becomes 'draft' — the video imports but is not
// exposed until the operator re-processes it.
func mapVideoState(ptState int) string {
	if ptState == 1 {
		return "published"
	}
	return "draft"
}

// intPtrToText renders an optional PeerTube numeric taxonomy id (category /
// licence) as the text form Vidra stores (migration 0025: "PeerTube-compatible
// numeric ids, as text"). nil stays nil (unset).
func intPtrToText(v *int) *string {
	if v == nil {
		return nil
	}
	s := strconv.Itoa(*v)
	return &s
}
