package ipfsmirror

import "testing"

// TestEligibilityTable is THE privacy fence (spec §3, §11): every media class is
// asserted across {public, private, unlisted, quarantined} — enqueue for the
// public case, NOT enqueue for every non-public case — and every never-mirror
// class (DM attachments, exports, upload chunks, live edge, remote thumbnails) is
// asserted NEVER enqueued regardless of any facts. If a new media class or
// visibility rule is added, extend this table; a public IPFS pin of non-public
// bytes must be a failing test, never a silent regression.
func TestEligibilityTable(t *testing.T) {
	// The four canonical visibility columns applied to every video-derived class.
	// "quarantined" is modeled as state='quarantined' with a public privacy value,
	// proving the state half of the gate (not just privacy) blocks it.
	videoClasses := []MediaClass{
		ClassVideoOriginal, ClassHLS, ClassWebM, ClassThumbnail,
		ClassStoryboard, ClassStoryboardVTT, ClassCaption,
	}
	videoCols := []struct {
		name           string
		privacy, state string
		want           bool
	}{
		{"public", "public", "published", true},
		{"private", "private", "published", false},
		{"unlisted", "unlisted", "published", false},
		{"quarantined", "public", "quarantined", false},
		// Extra non-public states that must also be refused even when privacy=public.
		{"draft", "public", "draft", false},
		{"processing", "public", "processing", false},
		{"scheduled", "public", "scheduled", false},
		{"failed", "public", "failed", false},
	}
	for _, cls := range videoClasses {
		for _, col := range videoCols {
			got := Eligible(Subject{Class: cls, VideoPrivacy: col.privacy, VideoState: col.state})
			if got != col.want {
				t.Errorf("video class %q [%s privacy=%s state=%s]: Eligible=%v, want %v",
					cls, col.name, col.privacy, col.state, got, col.want)
			}
		}
	}

	// Owner-unlisted dimension for video-derived classes (spec §3/§7): unlisted is
	// treated as PRIVATE for mirroring, so an unlisted owner's video is refused even
	// when the video itself is public+published. The full matrix is video-visibility
	// × owner{listed,unlisted}: only public+published AND owner-listed may mirror.
	for _, cls := range videoClasses {
		for _, col := range videoCols {
			for _, ownerUnlisted := range []bool{false, true} {
				// Eligible only when the video passes AND the owner is listed.
				want := col.want && !ownerUnlisted
				got := Eligible(Subject{Class: cls, VideoPrivacy: col.privacy, VideoState: col.state, OwnerUnlisted: ownerUnlisted})
				if got != want {
					t.Errorf("video class %q [%s privacy=%s state=%s owner_unlisted=%v]: Eligible=%v, want %v",
						cls, col.name, col.privacy, col.state, ownerUnlisted, got, want)
				}
			}
		}
	}

	// Identity images: gated by the owning account's active+unlisted flags. "public"
	// = active & listed → mirror; "unlisted" and a deactivated account → refuse.
	identityClasses := []MediaClass{
		ClassUserAvatar, ClassUserBanner, ClassChannelAvatar, ClassChannelBanner,
	}
	identityCols := []struct {
		name             string
		active, unlisted bool
		want             bool
	}{
		{"public", true, false, true},
		{"unlisted", true, true, false},
		{"deactivated", false, false, false},
		{"deactivated+unlisted", false, true, false},
	}
	for _, cls := range identityClasses {
		for _, col := range identityCols {
			got := Eligible(Subject{Class: cls, OwnerActive: col.active, OwnerUnlisted: col.unlisted})
			if got != col.want {
				t.Errorf("identity class %q [%s active=%v unlisted=%v]: Eligible=%v, want %v",
					cls, col.name, col.active, col.unlisted, got, col.want)
			}
		}
	}

	// Playlist cover: gated by playlist visibility only.
	playlistCols := []struct {
		visibility string
		want       bool
	}{
		{"public", true},
		{"unlisted", false},
		{"private", false},
		{"", false},
	}
	for _, col := range playlistCols {
		got := Eligible(Subject{Class: ClassPlaylistCover, PlaylistVisibility: col.visibility})
		if got != col.want {
			t.Errorf("playlist_cover [visibility=%s]: Eligible=%v, want %v", col.visibility, got, col.want)
		}
	}

	// NEVER-MIRROR classes: refused for every combination of facts. We throw the
	// most-permissive-looking facts at them (public video, active listed owner,
	// public playlist) to prove the class itself — not just missing facts — is what
	// bars them.
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
		if Eligible(s) {
			t.Errorf("never-mirror class %q: Eligible=true even with permissive facts, want false (privacy fence breach)", cls)
		}
	}
}
