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
	args := thumbnailArgs(localSource("/in.mp4"), "/out.jpg", 1)
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

func TestThumbnailAtArgs(t *testing.T) {
	cases := []struct {
		at   float64
		want string
	}{
		{0, "-y -ss 0 -i /in.mp4 -frames:v 1 -vf scale=640:-2 -q:v 3 /out.jpg"},
		{5, "-y -ss 5 -i /in.mp4 -frames:v 1 -vf scale=640:-2 -q:v 3 /out.jpg"},
		{5.5, "-y -ss 5.5 -i /in.mp4 -frames:v 1 -vf scale=640:-2 -q:v 3 /out.jpg"},
		{12.25, "-y -ss 12.25 -i /in.mp4 -frames:v 1 -vf scale=640:-2 -q:v 3 /out.jpg"},
	}
	for _, tc := range cases {
		args := thumbnailAtArgs(localSource("/in.mp4"), "/out.jpg", tc.at)
		if got := strings.Join(args, " "); got != tc.want {
			t.Errorf("thumbnailAtArgs(%v) = %q, want %q", tc.at, got, tc.want)
		}
		// The timestamp is a single -ss argument and the source/dest stay positional
		// (never interpolated into -vf), so the URL/time can never inject a flag.
		if args[1] != "-ss" {
			t.Errorf("thumbnailAtArgs(%v): second arg = %q, want -ss", tc.at, args[1])
		}
		if args[len(args)-1] != "/out.jpg" {
			t.Errorf("thumbnailAtArgs(%v): last arg = %q, want the output path", tc.at, args[len(args)-1])
		}
	}
}
