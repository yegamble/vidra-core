package peertubeimport

import (
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/profileimage"
)

func TestMapActorImageKind(t *testing.T) {
	if kind, ok := mapActorImageKind(1); !ok || kind != profileimage.KindAvatar {
		t.Fatalf("type 1 = %q,%v; want avatar,true", kind, ok)
	}
	if kind, ok := mapActorImageKind(2); !ok || kind != profileimage.KindBanner {
		t.Fatalf("type 2 = %q,%v; want banner,true", kind, ok)
	}
	for _, unknown := range []int{0, 3, -1, 99} {
		if kind, ok := mapActorImageKind(unknown); ok {
			t.Fatalf("type %d mapped to %q; unknown types must be unsupported, never guessed into a slot", unknown, kind)
		}
	}
}

func TestDeriveSourceOrigin(t *testing.T) {
	tests := []struct {
		name string
		urls []string
		want string
	}{
		{"account url", []string{"https://tube.example/accounts/alice"}, "https://tube.example"},
		{"channel url", []string{"https://tube.example/video-channels/news"}, "https://tube.example"},
		{"port is part of the origin", []string{"http://tube.example:9000/accounts/a"}, "http://tube.example:9000"},
		{
			"one odd row cannot steer the run",
			[]string{
				"https://evil.example/accounts/x",
				"https://tube.example/accounts/a",
				"https://tube.example/accounts/b",
				"https://tube.example/video-channels/c",
			},
			"https://tube.example",
		},
		{"ties keep the first seen", []string{"https://a.example/accounts/x", "https://b.example/accounts/y"}, "https://a.example"},
		{"unusable rows are ignored", []string{"", "   ", "not a url", "ftp://tube.example/x", "/accounts/relative"}, ""},
		{"nothing at all", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveSourceOrigin(tc.urls); got != tc.want {
				t.Fatalf("deriveSourceOrigin = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestImageExtForSniffedType(t *testing.T) {
	for ct, want := range map[string]string{
		"image/jpeg":    ".jpg",
		"image/png":     ".png",
		"image/webp":    ".webp",
		"IMAGE/PNG":     ".png",
		"image/gif":     "", // Vidra's own upload path does not accept it either
		"image/svg+xml": "",
		"text/html":     "",
		"":              "",
	} {
		if got := imageExtForSniffedType(ct); got != want {
			t.Fatalf("imageExtForSniffedType(%q) = %q, want %q", ct, got, want)
		}
	}
}

func TestActorImageFingerprint(t *testing.T) {
	a := profileimage.Image{StorageKey: "avatars/users/u.png", SizeBytes: 100}
	// The key alone cannot be the fingerprint: it is derived from the Vidra id and
	// the extension, so a person re-uploading a different picture of the same type
	// lands on exactly the same key.
	sameKeyOtherPicture := profileimage.Image{StorageKey: "avatars/users/u.png", SizeBytes: 2048}
	if actorImageFingerprint(a) == actorImageFingerprint(sameKeyOtherPicture) {
		t.Fatal("two different objects at the same key share a fingerprint; the import would claim a picture it did not write")
	}
	if got, want := actorImageFingerprint(a), actorImageFingerprint(profileimage.Image{StorageKey: "avatars/users/u.png", SizeBytes: 100}); got != want {
		t.Fatalf("fingerprint is not stable: %q vs %q", got, want)
	}
	if actorImageFingerprint(profileimage.Image{}) != "" {
		t.Fatal("an empty slot must fingerprint as empty, never as a value the import could match")
	}
}

// The replace-versus-clobber judgement, which is the whole of part (b): an
// import may update an image IT wrote and must never touch one a person put
// there — unless the operator has said the source wins.
func TestDecideActorImage(t *testing.T) {
	const (
		ours   = "avatars/users/u.png|1000"
		theirs = "avatars/users/u.png|4096"
	)
	tests := []struct {
		name            string
		slot            actorImageSlot
		gapFill         actorImageAction
		sourceAuthority actorImageAction
		why             string
	}{
		{
			name:            "an empty slot is a gap and gaps are what an import fills",
			slot:            actorImageSlot{},
			gapFill:         actorImageWrite,
			sourceAuthority: actorImageWrite,
		},
		{
			name:            "the slot holds this very carry: nothing is fetched and nothing is written",
			slot:            actorImageSlot{present: true, current: ours, carried: ours, wroteBefore: true, untouched: true, carriedThisFile: true},
			gapFill:         actorImageUpToDate,
			sourceAuthority: actorImageUpToDate,
			why:             "an unchanged source must cost neither an HTTP request nor an object PUT",
		},
		{
			name:            "the slot holds the import's own image and the source has moved to a better variant",
			slot:            actorImageSlot{present: true, current: ours, carried: ours, wroteBefore: true, untouched: true},
			gapFill:         actorImageReplace,
			sourceAuthority: actorImageReplace,
		},
		{
			name:            "a person uploaded over the import's image",
			slot:            actorImageSlot{present: true, current: theirs, carried: ours, wroteBefore: true},
			gapFill:         actorImageOperatorOwned,
			sourceAuthority: actorImageReplace,
			why:             "the fingerprint no longer matches, so the picture is somebody's",
		},
		{
			name:            "a person's avatar the import never touched",
			slot:            actorImageSlot{present: true, current: theirs},
			gapFill:         actorImageOperatorOwned,
			sourceAuthority: actorImageReplace,
		},
		{
			name:            "carried before the fingerprint memory existed, and untouched since",
			slot:            actorImageSlot{present: true, current: theirs, wroteBefore: true, untouched: true},
			gapFill:         actorImageReplace,
			sourceAuthority: actorImageReplace,
			why:             "the one-time self-heal: this is how 137 thumbnail avatars become the originals",
		},
		{
			name:            "carried before the memory existed, but written since",
			slot:            actorImageSlot{present: true, current: theirs, wroteBefore: true},
			gapFill:         actorImageOperatorOwned,
			sourceAuthority: actorImageReplace,
			why:             "the image row is newer than the ledger row, so a person wrote it",
		},
		{
			name:            "the import filled this slot and it is empty again",
			slot:            actorImageSlot{carried: ours, wroteBefore: true},
			gapFill:         actorImageCleared,
			sourceAuthority: actorImageWrite,
			why:             "removing an avatar is a decision, not a gap to refill every night",
		},
		{
			name:            "carried this very file, and the slot is empty",
			slot:            actorImageSlot{wroteBefore: true, carriedThisFile: true},
			gapFill:         actorImageCleared,
			sourceAuthority: actorImageWrite,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := decideActorImage(tc.slot, modeGapFill); got != tc.gapFill {
				t.Errorf("gap-fill = %v, want %v (%s)", got, tc.gapFill, tc.why)
			}
			if got := decideActorImage(tc.slot, modeSourceAuthoritative); got != tc.sourceAuthority {
				t.Errorf("source-authoritative = %v, want %v (%s)", got, tc.sourceAuthority, tc.why)
			}
		})
	}
}

// A ledger note names the chosen resolution — "which variant did you pick" is
// the question this bug made somebody ask of a finished migration — and never
// the source filename, because a note is read by whoever reads the ledger.
func TestActorImageLedgerNote(t *testing.T) {
	t.Run("the chosen resolution is in it", func(t *testing.T) {
		tgt := actorImageTarget{
			img:       SourceActorImage{Filename: "secret-looking-name.png", Width: 1500, Height: 1500},
			sourceID:  "7",
			imageKind: profileimage.KindAvatar,
			action:    actorImageWrite,
		}
		if got := tgt.note(); !strings.Contains(got, "1500x1500") {
			t.Errorf("note = %q, want the carried variant's size in it", got)
		}
		tgt.action = actorImageReplace
		if got := tgt.note(); !strings.Contains(got, "1500x1500") || !strings.Contains(got, "replaced") {
			t.Errorf("note = %q, want a replacement note naming the variant", got)
		}
	})
	t.Run("a source that records no size still identifies the row", func(t *testing.T) {
		tgt := actorImageTarget{sourceID: "7", imageKind: profileimage.KindAvatar, action: actorImageWrite}
		if got := tgt.note(); !strings.Contains(got, "source-image-7") {
			t.Errorf("note = %q, want the source row id when there is no size to name", got)
		}
	})
	t.Run("never the source filename", func(t *testing.T) {
		for _, action := range []actorImageAction{
			actorImageWrite, actorImageReplace, actorImageUpToDate, actorImageOperatorOwned, actorImageCleared,
		} {
			tgt := actorImageTarget{
				img:       SourceActorImage{Filename: "leaky.png", Width: 64, Height: 64},
				sourceID:  "7",
				imageKind: profileimage.KindAvatar,
				action:    action,
			}
			if strings.Contains(tgt.note(), "leaky.png") {
				t.Errorf("action %v leaked the source filename into the ledger note: %q", action, tgt.note())
			}
		}
	})
}

// The seam the repeated-sync workflow plugs into: identical inputs, mode
// flipped, opposite outcome. Nothing else about the decision changes — and in
// particular the mode does NOT turn "already carried" into a re-fetch, so the
// expensive half is still spent only where the two sides differ.
func TestDecideActorImageModeIsTheOnlySwitch(t *testing.T) {
	operatorOwned := actorImageSlot{present: true, current: "avatars/users/u.png|4096", carried: "avatars/users/u.png|1000", wroteBefore: true}
	if got := decideActorImage(operatorOwned, modeGapFill); got != actorImageOperatorOwned {
		t.Fatalf("gap-fill = %v, want the image left alone", got)
	}
	if got := decideActorImage(operatorOwned, modeSourceAuthoritative); got != actorImageReplace {
		t.Fatalf("source-authoritative = %v, want the same image replaced", got)
	}

	unchanged := actorImageSlot{present: true, current: "avatars/users/u.png|1000", carried: "avatars/users/u.png|1000", wroteBefore: true, untouched: true, carriedThisFile: true}
	for _, mode := range []importMode{modeGapFill, modeSourceAuthoritative} {
		if got := decideActorImage(unchanged, mode); got != actorImageUpToDate {
			t.Fatalf("mode %v turned an unchanged slot into %v; neither mode may re-fetch what is already there", mode, got)
		}
	}
}
