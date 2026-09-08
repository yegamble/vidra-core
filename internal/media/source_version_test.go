package media

import (
	"testing"

	"github.com/google/uuid"
)

// TestSourceVersionKeys covers the source-version model's key scheme: version 0
// keeps the legacy layout and replacement N gets a .rN tag. The OUTPUT prefix
// is no longer part of it — since migration 0136 it follows the transcode
// GENERATION instead, which is the subject of TestGenerationPrefixes below.
func TestSourceVersionKeys(t *testing.T) {
	id := uuid.MustParse("11111111-2222-3333-4444-555555555555")

	if got, want := OriginalVideoKey(id, 0, ".mp4"), "web-videos/"+id.String()+".mp4"; got != want {
		t.Errorf("v0 key = %q, want %q", got, want)
	}
	if got, want := OriginalVideoKey(id, 3, ".webm"), "web-videos/"+id.String()+".r3.webm"; got != want {
		t.Errorf("v3 key = %q, want %q", got, want)
	}

	// Round trip: version parses back out of the key.
	for _, v := range []int{0, 1, 2, 17} {
		key := OriginalVideoKey(id, v, ".mp4")
		if got := OriginalKeyVersion(key); got != v {
			t.Errorf("OriginalKeyVersion(%q) = %d, want %d", key, got, v)
		}
	}
	// Unparseable / legacy shapes are version 0.
	for _, key := range []string{
		"web-videos/" + id.String() + ".mp4",
		"web-videos/" + id.String() + ".r0.mp4", // r0 is not a valid tag
		"web-videos/whatever.bin",
		"",
	} {
		if got := OriginalKeyVersion(key); got != 0 {
			t.Errorf("OriginalKeyVersion(%q) = %d, want 0", key, got)
		}
	}

	if HLSGenerationName(0) != "" || HLSGenerationName(-1) != "" {
		t.Error("HLSGenerationName(<=0) must be empty (legacy layout)")
	}
	if got := HLSGenerationName(4); got != "r4" {
		t.Errorf("HLSGenerationName(4) = %q, want r4", got)
	}
	for seg, want := range map[string]bool{
		"r1": true, "r42": true,
		"r0": false, "720p": false, "master.m3u8": false, "peertube": false, "r": false, "r01": false,
	} {
		if got := IsHLSGenerationName(seg); got != want {
			t.Errorf("IsHLSGenerationName(%q) = %v, want %v", seg, got, want)
		}
	}

}

// TestGenerationPrefixesAddressEveryRun is the invariant migration 0136 exists
// for: two transcode runs of the SAME source must not share an output prefix.
//
// Before it, the prefix came from the source key's version, so a re-transcode
// of an unchanged source stayed at version 0 and rewrote the same fourteen
// objects. A32/A33 measured what that costs behind a cache — new bytes at the
// origin, the old ones still served on a HIT, the stale segment decoding with
// no error — and the fix has to hold for BOTH trees a run writes, because they
// are produced in the same window by the same job.
func TestGenerationPrefixesAddressEveryRun(t *testing.T) {
	id := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	hls := "streaming-playlists/" + id.String()
	web := "web-videos/" + id.String()

	// Generation 0 is the legacy in-place layout every pre-0136 tree lives at.
	if got := HLSPrefixForGeneration(id, 0); got != hls {
		t.Errorf("generation 0 HLS prefix = %q, want %q", got, hls)
	}
	if got := WebVideoPrefixForGeneration(id, 0); got != web {
		t.Errorf("generation 0 web-video prefix = %q, want %q", got, web)
	}

	// Every later generation is its own directory, in both trees, and the two
	// carry the SAME number so a run's outputs cannot drift apart.
	seenHLS := map[string]bool{hls: true}
	seenWeb := map[string]bool{web: true}
	for gen := 1; gen <= 5; gen++ {
		h, w := HLSPrefixForGeneration(id, gen), WebVideoPrefixForGeneration(id, gen)
		if want := hls + "/r" + string(rune('0'+gen)); h != want {
			t.Errorf("generation %d HLS prefix = %q, want %q", gen, h, want)
		}
		if want := web + "/r" + string(rune('0'+gen)); w != want {
			t.Errorf("generation %d web-video prefix = %q, want %q", gen, w, want)
		}
		if seenHLS[h] || seenWeb[w] {
			t.Fatalf("generation %d reuses a prefix a previous run already wrote: %q / %q", gen, h, w)
		}
		seenHLS[h], seenWeb[w] = true, true
	}

	// And the generation is INDEPENDENT of the source version, which is the
	// whole point: a re-transcode of an unchanged source advances one and not
	// the other. mediagc parses the directory name either way.
	if !IsHLSGenerationName("r3") || IsHLSGenerationName("720p") {
		t.Error("mediagc can no longer tell a generation directory from a rendition directory")
	}
}
