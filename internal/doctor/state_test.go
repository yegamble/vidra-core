package doctor

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTruncateNeverSplitsARune is the regression for byte-slicing subprocess
// output. firstLine feeds docker/compose stderr through truncate before it is
// printed on a ⚠ line, and that text is not ours: a container name, an image
// tag, or an operator's own path can be non-ASCII. Cutting at s[:n-1] lands
// mid-codepoint and the diagnostic renders as mojibake — exactly when the
// operator is trying to read it.
func TestTruncateNeverSplitsARune(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
	}{
		{"cut inside a three byte rune", strings.Repeat("日", 10), 9},
		{"cut inside a two byte rune", strings.Repeat("é", 10), 8},
		{"cut inside an emoji", "aaa" + strings.Repeat("🙂", 5), 6},
		{"cut exactly on a boundary", strings.Repeat("日", 10), 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.in, tc.n)
			if !utf8.ValidString(got) {
				t.Fatalf("truncate(%q, %d) = %q, which is not valid UTF-8", tc.in, tc.n, got)
			}
			if !strings.HasSuffix(got, "…") {
				t.Errorf("truncate(%q, %d) = %q, want a trailing ellipsis on an over-length string", tc.in, tc.n, got)
			}
			if body := strings.TrimSuffix(got, "…"); !strings.HasPrefix(tc.in, body) {
				t.Errorf("truncate(%q, %d) = %q, whose body is not a prefix of the input", tc.in, tc.n, got)
			}
		})
	}
}

// TestTruncateKeepsShortStringsWhole pins the no-op case: at or under the cap
// nothing is cut and no ellipsis is added.
func TestTruncateKeepsShortStringsWhole(t *testing.T) {
	for _, s := range []string{"", "abc", "日本語", "🙂🙂"} {
		if got := truncate(s, 220); got != s {
			t.Errorf("truncate(%q, 220) = %q, want it unchanged", s, got)
		}
	}
}
