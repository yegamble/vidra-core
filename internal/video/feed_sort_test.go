package video

import "testing"

func TestNormalizeFeedSort(t *testing.T) {
	for _, ok := range []string{"recent", "popular", "trending", "likes", "originally_published_at", "name"} {
		if got := NormalizeFeedSort(ok); got != ok {
			t.Errorf("NormalizeFeedSort(%q) = %q, want it kept", ok, got)
		}
	}
	// "hot" is deliberately not offered (see feedSorts); junk falls back.
	for _, bad := range []string{"", "hot", "-likes", "NAME", "views; drop table"} {
		if got := NormalizeFeedSort(bad); got != "recent" {
			t.Errorf("NormalizeFeedSort(%q) = %q, want recent", bad, got)
		}
	}
}
