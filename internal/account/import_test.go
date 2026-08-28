package account

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTruncateNeverSplitsARune is the regression for byte-slicing untrusted
// text. An imported archive comes from anywhere, and its display name, bio and
// playlist titles are bounded here before they reach the database. Cutting at
// s[:max] lands mid-codepoint whenever the boundary falls inside a multi-byte
// character, and the stored profile then carries a replacement character that
// the owner never typed and cannot remove.
func TestTruncateNeverSplitsARune(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
	}{
		{"cut inside a three byte rune", strings.Repeat("日", 10), 10},
		{"cut inside a two byte rune", strings.Repeat("é", 10), 7},
		{"cut inside an emoji", "aaa" + strings.Repeat("🙂", 5), 5},
		{"cut exactly on a boundary", strings.Repeat("日", 10), 9},
		{"ascii is untouched", strings.Repeat("a", 10), 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.in, tc.max)
			if !utf8.ValidString(got) {
				t.Fatalf("truncate(%q, %d) = %q, which is not valid UTF-8", tc.in, tc.max, got)
			}
			if len(got) > tc.max {
				t.Errorf("truncate(%q, %d) = %q (%d bytes), which exceeds the cap", tc.in, tc.max, got, len(got))
			}
			if !strings.HasPrefix(tc.in, got) {
				t.Errorf("truncate(%q, %d) = %q, which is not a prefix of the input", tc.in, tc.max, got)
			}
		})
	}
}

// TestTruncateKeepsShortStringsWhole pins the no-op case: under the cap, the
// string is returned unchanged, multi-byte or not.
func TestTruncateKeepsShortStringsWhole(t *testing.T) {
	for _, s := range []string{"", "abc", "日本語", "🙂🙂"} {
		if got := truncate(s, importMaxTitleLen); got != s {
			t.Errorf("truncate(%q, %d) = %q, want it unchanged", s, importMaxTitleLen, got)
		}
	}
}
