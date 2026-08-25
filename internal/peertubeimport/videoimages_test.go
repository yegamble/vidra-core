package peertubeimport

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The replace-versus-clobber judgement for a video's poster and storyboard. Two
// things are on trial here: that an import never writes over a poster a creator
// put there, and that it DOES repair the ~12–16k rows an earlier release left
// pointing at objects PeerTube never stored.
func TestDecideVideoImage(t *testing.T) {
	const (
		ours   = "thumbnails/v.jpg|1000"
		theirs = "thumbnails/v.jpg|4096"
	)
	// Every case below has bytes in the store unless it says otherwise; the
	// missing-object case is its own test.
	tests := []struct {
		name            string
		slot            videoImageSlot
		gapFill         videoImageAction
		sourceAuthority videoImageAction
		why             string
	}{
		{
			name:            "no poster at all is a gap and gaps are what an import fills",
			slot:            videoImageSlot{},
			gapFill:         videoImageWrite,
			sourceAuthority: videoImageWrite,
		},
		{
			name:            "the slot holds this very carry: nothing is fetched and nothing is written",
			slot:            videoImageSlot{present: true, bytesPresent: true, nativeKey: true, current: ours, carried: ours, wroteBefore: true, untouched: true, carriedThisFile: true},
			gapFill:         videoImageUpToDate,
			sourceAuthority: videoImageUpToDate,
			why:             "an unchanged source must cost neither an HTTP request nor an object PUT",
		},
		{
			name:            "the slot holds the import's own poster and the source has moved to another row",
			slot:            videoImageSlot{present: true, bytesPresent: true, nativeKey: true, current: ours, carried: ours, wroteBefore: true, untouched: true},
			gapFill:         videoImageReplace,
			sourceAuthority: videoImageReplace,
		},
		{
			name:            "a creator uploaded over the import's poster",
			slot:            videoImageSlot{present: true, bytesPresent: true, nativeKey: true, current: theirs, carried: ours, wroteBefore: true},
			gapFill:         videoImageOperatorOwned,
			sourceAuthority: videoImageReplace,
			why:             "the fingerprint no longer matches, so the poster is somebody's",
		},
		{
			name:            "a creator's poster the import never touched",
			slot:            videoImageSlot{present: true, bytesPresent: true, nativeKey: true, current: theirs},
			gapFill:         videoImageOperatorOwned,
			sourceAuthority: videoImageReplace,
			why:             "SetThumbnail lands on the native key, which is what stops the bridge claiming it",
		},
		{
			name:            "carried before the fingerprint memory existed, and untouched since",
			slot:            videoImageSlot{present: true, bytesPresent: true, nativeKey: true, current: theirs, wroteBefore: true, untouched: true},
			gapFill:         videoImageReplace,
			sourceAuthority: videoImageReplace,
		},
		{
			name:            "carried before the memory existed, but written since",
			slot:            videoImageSlot{present: true, bytesPresent: true, nativeKey: true, current: theirs, wroteBefore: true},
			gapFill:         videoImageOperatorOwned,
			sourceAuthority: videoImageReplace,
			why:             "the video_files row is newer than the ledger row, so a person wrote it",
		},
		{
			name:            "the import filled this slot and it is empty again",
			slot:            videoImageSlot{carried: ours, wroteBefore: true},
			gapFill:         videoImageCleared,
			sourceAuthority: videoImageWrite,
			why:             "removing a poster is a decision, not a gap to refill every night",
		},
		{
			name:            "carried this very row, and the slot is empty",
			slot:            videoImageSlot{wroteBefore: true, carriedThisFile: true},
			gapFill:         videoImageCleared,
			sourceAuthority: videoImageWrite,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := decideVideoImage(tc.slot, modeGapFill); got != tc.gapFill {
				t.Errorf("gap-fill = %v, want %v (%s)", got, tc.gapFill, tc.why)
			}
			if got := decideVideoImage(tc.slot, modeSourceAuthoritative); got != tc.sourceAuthority {
				t.Errorf("source-authoritative = %v, want %v (%s)", got, tc.sourceAuthority, tc.why)
			}
		})
	}
}

// The live failure, and the one-time bridge that repairs it: a kind='thumbnail'
// row at a key that is not the native one, with NO object behind it, is what an
// earlier release wrote for every video on an instance migrated in reference
// mode. has_thumbnail said true and GET /thumbnail 404'd on 40 of 40 sampled.
func TestDecideVideoImageBridgesTheOldImportersRows(t *testing.T) {
	t.Run("a non-native key with no object is replaced", func(t *testing.T) {
		broken := videoImageSlot{
			present:      true,
			bytesPresent: false, // thumbnails/<peertube-filename> was never stored
			nativeKey:    false,
			current:      "thumbnails/abcdef.jpg|0",
		}
		for _, mode := range []importMode{modeGapFill, modeSourceAuthoritative} {
			if got := decideVideoImage(broken, mode); got != videoImageReplace {
				t.Fatalf("mode %v = %v, want the broken row replaced; nobody owns bytes that are not there", mode, got)
			}
		}
	})

	t.Run("a non-native key whose object IS there is adopted, not re-fetched", func(t *testing.T) {
		// --source-local-root copy mode: the old importer wrote
		// thumbnails/<random-uuid>.jpg AND the bytes. That configuration was
		// correct all along and must not regress.
		working := videoImageSlot{
			present:      true,
			bytesPresent: true,
			nativeKey:    false,
			current:      "thumbnails/1e0c2b6e-....jpg|4096",
		}
		if got := decideVideoImage(working, modeGapFill); got != videoImageUpToDate {
			t.Fatalf("gap-fill = %v, want the working poster left exactly as it is", got)
		}
	})

	t.Run("the native key with an object is left alone in gap-fill", func(t *testing.T) {
		// A creator who uploads a poster after the import goes through
		// SetThumbnail and lands on the native key. The bridge must not claim it.
		native := videoImageSlot{
			present:      true,
			bytesPresent: true,
			nativeKey:    true,
			current:      "thumbnails/" + uuid.New().String() + ".jpg|9000",
		}
		if got := decideVideoImage(native, modeGapFill); got != videoImageOperatorOwned {
			t.Fatalf("gap-fill = %v, want the creator's poster left unchanged and reported", got)
		}
	})
}

// The seam the repeated-sync workflow plugs into: identical inputs, mode
// flipped, opposite outcome — and the mode changes NOTHING else. In particular
// it does not turn "already carried" into a re-fetch, so the expensive half is
// still spent only where the two sides differ.
func TestDecideVideoImageModeIsTheOnlySwitch(t *testing.T) {
	operatorOwned := videoImageSlot{
		present: true, bytesPresent: true, nativeKey: true,
		current: "thumbnails/v.jpg|4096", carried: "thumbnails/v.jpg|1000", wroteBefore: true,
	}
	if got := decideVideoImage(operatorOwned, modeGapFill); got != videoImageOperatorOwned {
		t.Fatalf("gap-fill = %v, want the poster left alone", got)
	}
	if got := decideVideoImage(operatorOwned, modeSourceAuthoritative); got != videoImageReplace {
		t.Fatalf("source-authoritative = %v, want the same poster replaced", got)
	}

	unchanged := videoImageSlot{
		present: true, bytesPresent: true, nativeKey: true,
		current: "thumbnails/v.jpg|1000", carried: "thumbnails/v.jpg|1000",
		wroteBefore: true, untouched: true, carriedThisFile: true,
	}
	for _, mode := range []importMode{modeGapFill, modeSourceAuthoritative} {
		if got := decideVideoImage(unchanged, mode); got != videoImageUpToDate {
			t.Fatalf("mode %v turned an unchanged slot into %v; neither mode may re-fetch what is already there", mode, got)
		}
	}

	adopted := videoImageSlot{present: true, bytesPresent: true, current: "thumbnails/old.jpg|2048"}
	for _, mode := range []importMode{modeGapFill, modeSourceAuthoritative} {
		if got := decideVideoImage(adopted, mode); got != videoImageUpToDate {
			t.Fatalf("mode %v = %v; a working poster the import itself wrote is not a divergence to resolve", mode, got)
		}
	}
}

func TestVideoImageFingerprint(t *testing.T) {
	// The key alone cannot be the fingerprint: it is derived from the video id, so
	// a replacement lands on exactly the same key.
	if videoImageFingerprint("thumbnails/v.jpg", 100) == videoImageFingerprint("thumbnails/v.jpg", 2048) {
		t.Fatal("two different objects at the same key share a fingerprint; the import would claim a poster it did not write")
	}
	if a, b := videoImageFingerprint("thumbnails/v.jpg", 100), videoImageFingerprint("thumbnails/v.jpg", 100); a != b {
		t.Fatalf("fingerprint is not stable: %q vs %q", a, b)
	}
	if videoImageFingerprint("", 100) != "" {
		t.Fatal("an empty slot must fingerprint as empty, never as a value the import could match")
	}
}

// A ledger note is read by whoever reads the ledger, so it never carries a
// source filename or a storage key.
func TestVideoImageLedgerNote(t *testing.T) {
	for _, action := range []videoImageAction{
		videoImageWrite, videoImageReplace, videoImageUpToDate, videoImageOperatorOwned, videoImageCleared,
	} {
		tgt := videoImageTarget{
			row:      videoImageRow{filename: "leaky-source-name.jpg"},
			kind:     KindThumbnail,
			what:     "thumbnail",
			sourceID: "7",
			action:   action,
		}
		note := tgt.note()
		if note == "" {
			t.Errorf("action %v renders no note", action)
		}
		if strings.Contains(note, "leaky-source-name.jpg") {
			t.Errorf("action %v leaked the source filename into the ledger note: %q", action, note)
		}
	}
}

// storyboardPlan is the seam between the source's geometry columns and the
// WebVTT map Vidra has to synthesise, because PeerTube stores no VTT of its own.
func TestStoryboardPlanFromASourceRow(t *testing.T) {
	row := videoImageRow{
		board: SourceStoryboard{
			TotalWidth: 192 * 11, TotalHeight: 108 * 11,
			SpriteWidth: 192, SpriteHeight: 108, SpriteDuration: 3,
		},
		duration: 300,
	}
	plan, ok := storyboardPlan(row)
	if !ok {
		t.Fatal("a well-formed source row must produce a plan")
	}
	if plan.Tiles != 100 {
		t.Fatalf("tiles = %d, want ceil(300/3); the 121-cell grid is padded with black", plan.Tiles)
	}

	// A video PeerTube never storyboarded, or one whose duration the source does
	// not record: unsupported, not a scrub bar full of black frames.
	row.duration = 0
	if _, ok := storyboardPlan(row); ok {
		t.Fatal("a video with no duration must not produce a plan")
	}
	row.duration = 300
	row.board.SpriteDuration = 0
	if _, ok := storyboardPlan(row); ok {
		t.Fatal("a row with no sprite duration must not produce a plan")
	}
}
