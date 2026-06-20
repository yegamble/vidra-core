package media

import (
	"strings"
	"testing"
)

func TestThumbnailSeekSeconds(t *testing.T) {
	cases := map[int]int{
		0:    0, // unknown -> first frame
		1:    0, // sub-2s -> first frame
		2:    1,
		10:   1,
		3600: 1,
	}
	for dur, want := range cases {
		if got := thumbnailSeekSeconds(dur); got != want {
			t.Errorf("thumbnailSeekSeconds(%d) = %d, want %d", dur, got, want)
		}
	}
}

func TestThumbnailArgs(t *testing.T) {
	args := thumbnailArgs("/in.mp4", "/out.jpg", 1)
	got := strings.Join(args, " ")
	want := "-y -ss 1 -i /in.mp4 -frames:v 1 -vf scale=640:-2 -q:v 3 /out.jpg"
	if got != want {
		t.Errorf("thumbnailArgs = %q, want %q", got, want)
	}
	// Source and destination are passed positionally (not interpolated into -vf).
	if args[len(args)-1] != "/out.jpg" {
		t.Errorf("last arg = %q, want the output path", args[len(args)-1])
	}
}
