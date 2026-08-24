package peertubeimport

import (
	"math"
	"strconv"
	"strings"
)

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

// mapRating maps a PeerTube accountVideoRate.type to Vidra's video_ratings.rating
// CHECK. PeerTube writes 'like' and 'dislike'; anything else (a 'none' row left
// behind when a user cleared their rating, or a value from a schema this tool
// has not seen) is NOT a rating and is reported as unsupported rather than
// coerced into a like.
func mapRating(ptType string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(ptType)) {
	case "like":
		return "like", true
	case "dislike":
		return "dislike", true
	default:
		return "", false
	}
}

// normalizeChapterTitle fits a source chapter title to video_chapters' CHECK
// (1..120 characters). It trims, then truncates by RUNE — the constraint counts
// characters, so a byte-slice would both cut a multi-byte title short of the
// limit and risk splitting a rune. "" means the source title was blank, which is
// not a chapter Vidra can store.
func normalizeChapterTitle(title string) string {
	t := strings.TrimSpace(title)
	if t == "" {
		return ""
	}
	const maxChapterTitle = 120
	if runes := []rune(t); len(runes) > maxChapterTitle {
		t = strings.TrimSpace(string(runes[:maxChapterTitle]))
	}
	return t
}

// renditionHeight is the rung's pixel height: the height the source recorded for
// the stored file when it has one, otherwise the rung's resolution label, which
// for every non-audio PeerTube rung IS its height ("720" = 720 lines).
func renditionHeight(resolution, sourceHeight int) int {
	if sourceHeight > 0 {
		return sourceHeight
	}
	return resolution
}

// renditionWidth is the rung's pixel width, in descending order of how much the
// source actually told us:
//
//  1. the width recorded against the stored file, when the source records one;
//  2. height x the video's recorded aspect ratio, when the source records that;
//  3. height x 16:9.
//
// Only (3) is a guess, and it is a guess the schema forces: video_renditions
// CHECKs width > 0, so there is no "unknown" to store. It is confined to the
// width — the height, which is what a quality menu labels a rung with and what
// a player picks on, is never guessed. A 4:3 video imported from a source that
// records neither dimension gets a too-wide width and a correct 480p label.
func renditionWidth(height, sourceWidth int, aspectRatio float64) int {
	if sourceWidth > 0 {
		return sourceWidth
	}
	if height <= 0 {
		return 0
	}
	ratio := aspectRatio
	if ratio <= 0 {
		ratio = 16.0 / 9.0
	}
	w := int(math.Round(float64(height) * ratio))
	if w%2 != 0 {
		w++
	}
	if w < 2 {
		w = 2
	}
	return w
}
