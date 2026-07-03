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
