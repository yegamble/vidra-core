package ipfsmirror

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestRouteTable is THE privacy fence (spec §4, eligibility matrix v2): every media
// class is asserted across visibility states AND the expected SWARM it routes to —
// NetworkPublic, NetworkPrivate, or NetworkNone (not mirrored anywhere). This is the
// class × privacy-state × network regression table the whole phase rests on: a public
// pin of non-public bytes, a private pin of quarantined/DM bytes, or ANY mirror of a
// never-mirror class must be a failing test, never a silent regression. Route decides
// the ELIGIBLE swarm from visibility alone; the tier-gate (effectiveNetwork) that can
// downgrade it to none when a tier is off is exercised in the service tests.
func TestRouteTable(t *testing.T) {
	videoClasses := []MediaClass{
		ClassVideoOriginal, ClassHLS, ClassWebM, ClassThumbnail,
		ClassStoryboard, ClassStoryboardVTT, ClassCaption,
	}
	// Video-derived routing: only a PUBLISHED video is mirrored at all. Published
	// public+listed → public; published private/unlisted, OR public+owner-unlisted →
	// private; every non-published state (quarantined/draft/processing/scheduled/
	// failed) → none regardless of privacy.
	videoCols := []struct {
		name          string
		privacy       string
		state         string
		ownerUnlisted bool
		want          string
	}{
		{"public+published+listed", "public", "published", false, NetworkPublic},
		{"public+published+unlisted-owner", "public", "published", true, NetworkPrivate},
		{"private+published", "private", "published", false, NetworkPrivate},
		{"private+published+unlisted-owner", "private", "published", true, NetworkPrivate},
		{"unlisted+published", "unlisted", "published", false, NetworkPrivate},
		{"unlisted+published+unlisted-owner", "unlisted", "published", true, NetworkPrivate},
		// quarantined → nowhere (unvetted content stays out of EVERY mirror).
		{"quarantined", "public", "quarantined", false, NetworkNone},
		{"private+quarantined", "private", "quarantined", false, NetworkNone},
		// Non-live/incomplete states → nowhere (picked up on the publish transition).
		{"draft", "public", "draft", false, NetworkNone},
		{"private+draft", "private", "draft", false, NetworkNone},
		{"processing", "public", "processing", false, NetworkNone},
		{"scheduled", "public", "scheduled", false, NetworkNone},
		{"failed", "public", "failed", false, NetworkNone},
	}
	for _, cls := range videoClasses {
		for _, col := range videoCols {
			got := Route(Subject{Class: cls, VideoPrivacy: col.privacy, VideoState: col.state, OwnerUnlisted: col.ownerUnlisted})
			if got != col.want {
				t.Errorf("video class %q [%s]: Route=%q, want %q", cls, col.name, got, col.want)
			}
		}
	}

	// Identity images: active+listed → public; unlisted OR deactivated → private.
	identityClasses := []MediaClass{
		ClassUserAvatar, ClassUserBanner, ClassChannelAvatar, ClassChannelBanner,
	}
	identityCols := []struct {
		name             string
		active, unlisted bool
		want             string
	}{
		{"active+listed", true, false, NetworkPublic},
		{"active+unlisted", true, true, NetworkPrivate},
		{"deactivated+listed", false, false, NetworkPrivate},
		{"deactivated+unlisted", false, true, NetworkPrivate},
	}
	for _, cls := range identityClasses {
		for _, col := range identityCols {
			got := Route(Subject{Class: cls, OwnerActive: col.active, OwnerUnlisted: col.unlisted})
			if got != col.want {
				t.Errorf("identity class %q [%s]: Route=%q, want %q", cls, col.name, got, col.want)
			}
		}
	}

	// Playlist cover: public playlist → public; any non-public visibility → private.
	playlistCols := []struct {
		visibility string
		want       string
	}{
		{"public", NetworkPublic},
		{"unlisted", NetworkPrivate},
		{"private", NetworkPrivate},
		{"", NetworkPrivate}, // unknown visibility defaults to the SAFER swarm, never public
	}
	for _, col := range playlistCols {
		got := Route(Subject{Class: ClassPlaylistCover, PlaylistVisibility: col.visibility})
		if got != col.want {
			t.Errorf("playlist_cover [visibility=%q]: Route=%q, want %q", col.visibility, got, col.want)
		}
	}

	// NEVER-MIRROR classes: NetworkNone on EITHER swarm, for every combination of
	// facts. DM attachments (plaintext today, and the future e2ee-blobs — a class that
	// does not exist yet) are asserted NEVER mirrored on either network per Messaging
	// v2 D7. We throw the most-permissive-looking facts at each to prove the class
	// itself — not just missing facts — bars them.
	neverClasses := []MediaClass{
		ClassDMAttachment, ClassAccountExport, ClassUploadChunk, ClassLiveEdge, ClassRemoteThumbnail,
		MediaClass("some_unknown_future_class"), // default-deny catch-all
	}
	permissive := Subject{
		VideoPrivacy: "public", VideoState: "published",
		OwnerActive: true, OwnerUnlisted: false,
		PlaylistVisibility: "public",
	}
	for _, cls := range neverClasses {
		s := permissive
		s.Class = cls
		if got := Route(s); got != NetworkNone {
			t.Errorf("never-mirror class %q: Route=%q even with permissive facts, want %q (privacy fence breach)",
				cls, got, NetworkNone)
		}
	}
}

// TestSweepVideoDerivedClassesMatchQuery keeps the eligibility backstop's SQL in step
// with this package's class model.
//
// SweepIneligibleIPFSPins branch 4 (A31) re-arms a video-derived ledger row whose
// video_id the delete FK nulled. It cannot JOIN its way to that judgement — the
// provenance is gone — so it names the video-derived classes literally, and a class
// added to isVideoDerived but not to that list would be an orphan the sweep silently
// walks past: bytes left pinned for a deleted video. This asserts the two lists are
// the same set, in the only place that knows both.
func TestSweepVideoDerivedClassesMatchQuery(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "store", "queries", "media_ipfs_pins.sql"))
	if err != nil {
		t.Fatalf("read query file: %v", err)
	}
	_, after, ok := strings.Cut(string(raw), "-- Branch 4 — THE DELETED VIDEO'S ORPHANS")
	if !ok {
		t.Fatal("branch 4 (the delete-orphan sweep) is gone from media_ipfs_pins.sql")
	}
	_, after, ok = strings.Cut(after, "p.media_class IN (")
	if !ok {
		t.Fatal("branch 4 no longer names the media classes it sweeps")
	}
	list, _, ok := strings.Cut(after, ")")
	if !ok {
		t.Fatal("branch 4's media_class IN (...) list is unreadable")
	}
	inQuery := map[MediaClass]bool{}
	for _, m := range regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(list, -1) {
		inQuery[MediaClass(m[1])] = true
	}

	// Every class this package knows, so a NEW constant is covered by construction.
	all := []MediaClass{
		ClassVideoOriginal, ClassHLS, ClassWebM, ClassThumbnail, ClassStoryboard,
		ClassStoryboardVTT, ClassCaption, ClassUserAvatar, ClassUserBanner,
		ClassChannelAvatar, ClassChannelBanner, ClassPlaylistCover,
		ClassDMAttachment, ClassAccountExport, ClassUploadChunk, ClassLiveEdge,
		ClassRemoteThumbnail,
	}
	for _, c := range all {
		if want, got := isVideoDerived(c), inQuery[c]; want != got {
			t.Errorf("class %q: isVideoDerived=%v but present in the sweep's branch-4 list=%v", c, want, got)
		}
		delete(inQuery, c)
	}
	for c := range inQuery {
		t.Errorf("the sweep's branch-4 list names %q, which is not a media class this package declares", c)
	}
}
