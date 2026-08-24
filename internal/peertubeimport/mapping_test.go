package peertubeimport

import (
	"strings"
	"testing"
)

func TestMapRole(t *testing.T) {
	cases := map[int]string{0: "admin", 1: "moderator", 2: "user", 99: "user"}
	for in, want := range cases {
		if got := mapRole(in); got != want {
			t.Errorf("mapRole(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestMapPrivacy(t *testing.T) {
	cases := map[int]string{1: "public", 2: "unlisted", 3: "private", 4: "private", 0: "private"}
	for in, want := range cases {
		if got := mapPrivacy(in); got != want {
			t.Errorf("mapPrivacy(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestMapPlaylistPrivacy(t *testing.T) {
	cases := map[int]string{1: "public", 2: "unlisted", 3: "private"}
	for in, want := range cases {
		if got := mapPlaylistPrivacy(in); got != want {
			t.Errorf("mapPlaylistPrivacy(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestMapVideoState(t *testing.T) {
	if mapVideoState(1) != "published" {
		t.Error("state 1 must map to published")
	}
	for _, s := range []int{0, 2, 3, 7} {
		if got := mapVideoState(s); got != "draft" {
			t.Errorf("mapVideoState(%d) = %q, want draft", s, got)
		}
	}
}

func TestIntPtrToText(t *testing.T) {
	if intPtrToText(nil) != nil {
		t.Error("nil in → nil out")
	}
	v := 5
	got := intPtrToText(&v)
	if got == nil || *got != "5" {
		t.Errorf("intPtrToText(&5) = %v, want \"5\"", got)
	}
}

func TestNormalizeTag(t *testing.T) {
	cases := map[string]string{
		"Music":  "music",
		"  hi  ": "hi",
		"":       "",
	}
	for in, want := range cases {
		if got := normalizeTag(in); got != want {
			t.Errorf("normalizeTag(%q) = %q, want %q", in, got, want)
		}
	}
	// Over-length tags are dropped.
	long := make([]byte, 60)
	for i := range long {
		long[i] = 'a'
	}
	if normalizeTag(string(long)) != "" {
		t.Error("over-length tag must be dropped")
	}
}

func TestReportCountsAndSummary(t *testing.T) {
	r := NewReport(true, PolicySkip)
	r.SourceVersion = 800
	if r.Entities[KindUser] == nil {
		t.Fatal("report must initialise every entity kind")
	}
	r.count(KindUser).Planned = 3
	r.count(KindVideo).Imported = 2
	sum := r.Summary()
	if sum == "" {
		t.Error("summary must be non-empty")
	}
	// Summary must not be empty and must reflect the dry-run + version.
	for _, want := range []string{"dry-run", "v800", "planned=3"} {
		if !strings.Contains(sum, want) {
			t.Errorf("summary %q missing %q", sum, want)
		}
	}
}

func TestMapRating(t *testing.T) {
	for _, in := range []string{"like", "LIKE", " like "} {
		if got, ok := mapRating(in); !ok || got != "like" {
			t.Errorf("mapRating(%q) = (%q,%v), want (like,true)", in, got, ok)
		}
	}
	if got, ok := mapRating("dislike"); !ok || got != "dislike" {
		t.Errorf("mapRating(dislike) = (%q,%v), want (dislike,true)", got, ok)
	}
	// A cleared rating in PeerTube is a 'none' row. Coercing it into a like would
	// invent an opinion the user retracted.
	for _, in := range []string{"none", "", "LOVE"} {
		if got, ok := mapRating(in); ok {
			t.Errorf("mapRating(%q) = (%q,true), want unsupported", in, got)
		}
	}
}

func TestNormalizeChapterTitle(t *testing.T) {
	if got := normalizeChapterTitle("  Intro  "); got != "Intro" {
		t.Errorf("normalizeChapterTitle trims: got %q", got)
	}
	if got := normalizeChapterTitle("   "); got != "" {
		t.Errorf("a blank title is not a chapter: got %q", got)
	}
	// video_chapters CHECKs char_length 1..120, which counts CHARACTERS. A
	// 200-rune multi-byte title must come back at 120 runes, not 120 bytes.
	long := strings.Repeat("é", 200)
	got := normalizeChapterTitle(long)
	if n := len([]rune(got)); n != 120 {
		t.Errorf("truncated title = %d runes, want 120", n)
	}
	if len(got) == 120 {
		t.Error("truncation counted bytes, not characters")
	}
}

func TestRenditionHeight(t *testing.T) {
	if got := renditionHeight(720, 0); got != 720 {
		t.Errorf("with no recorded height the resolution label IS the height: got %d", got)
	}
	// A source that records the real stored height wins over the label.
	if got := renditionHeight(720, 718); got != 718 {
		t.Errorf("recorded height must win: got %d", got)
	}
}

func TestRenditionWidth(t *testing.T) {
	// 1. the source's own width, when it has one — never overridden by a guess.
	if got := renditionWidth(480, 640, 16.0/9.0); got != 640 {
		t.Errorf("recorded width must win: got %d, want 640", got)
	}
	// 2. the video's recorded aspect ratio.
	if got := renditionWidth(480, 0, 4.0/3.0); got != 640 {
		t.Errorf("4:3 480p width = %d, want 640", got)
	}
	// 3. only then 16:9, and always even (an odd pixel width is not a thing an
	// encoder produces, and a UI reading it back would render 853x480 oddly).
	if got := renditionWidth(480, 0, 0); got != 854 {
		t.Errorf("16:9 480p width = %d, want 854 (rounded up to even)", got)
	}
	if got := renditionWidth(0, 0, 0); got != 0 {
		t.Errorf("no height means no width: got %d", got)
	}
}

func TestReportCarriesPerVideoKinds(t *testing.T) {
	r := NewReport(true, PolicySkip)
	// The JSON shape is a frontend contract: every kind is present from the start,
	// so a dry-run that plans nothing still reports zeroes rather than gaps.
	for _, kind := range []string{KindViewCount, KindChapter, KindRating, KindRendition} {
		if r.Entities[kind] == nil {
			t.Errorf("report must initialise %q", kind)
		}
	}
}
