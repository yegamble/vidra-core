package media

import (
	"testing"

	"github.com/google/uuid"
)

// TestSourceVersionKeys covers the W14 source-version model's key scheme:
// version 0 keeps the legacy layout, replacement N gets a .rN tag, and the
// HLS output prefix follows the SOURCE key's version so a re-transcode never
// overwrites the tree players are streaming.
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

	// The transcoder's output prefix follows the source version.
	legacy := HLSPrefixForSource(id, OriginalVideoKey(id, 0, ".mp4"))
	if want := "streaming-playlists/" + id.String(); legacy != want {
		t.Errorf("v0 prefix = %q, want %q", legacy, want)
	}
	gen := HLSPrefixForSource(id, OriginalVideoKey(id, 2, ".mkv"))
	if want := "streaming-playlists/" + id.String() + "/r2"; gen != want {
		t.Errorf("v2 prefix = %q, want %q", gen, want)
	}
}
