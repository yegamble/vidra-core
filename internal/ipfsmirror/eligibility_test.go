package ipfsmirror

import "testing"

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
