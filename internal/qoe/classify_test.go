package qoe

import "testing"

// TestClassifyOrigins walks the whole vocabulary, including the case that makes
// the vocabulary bounded at all: an origin nobody configured collapses into
// 'other' rather than becoming a dimension value of its own.
func TestClassifyOrigins(t *testing.T) {
	c := NewClassifier(
		"https://cdn.example.com/media",
		"https://gateway.example.org",
		"https://s3.example.net",
		"https://vidra.example.com",
	)
	cases := []struct {
		name string
		url  string
		want DeliverySource
	}{
		{"cdn exact", "https://cdn.example.com/media", SourceCDN},
		{"cdn under path", "https://cdn.example.com/media/videos/a/hls/master.m3u8", SourceCDN},
		{"cdn with cache buster", "https://cdn.example.com/media/videos/a/seg.ts?v=7", SourceCDN},
		{"ipfs gateway", "https://gateway.example.org/ipfs/bafy.../720p/seg.ts", SourceIPFSGateway},
		{"presigned object store", "https://s3.example.net/bucket/key?X-Amz-Signature=deadbeef", SourcePresigned},
		{"own origin", "https://vidra.example.com/api/v1/videos/a/hls/master.m3u8", SourceAPIProxy},
		{"origin-relative", "/api/v1/videos/a/hls/master.m3u8", SourceAPIProxy},
		{"host casing is irrelevant", "https://CDN.Example.COM/media/x.ts", SourceCDN},

		// The bounded-cardinality rule, stated as tests.
		{"unknown host", "https://someone-elses-cdn.test/media/x.ts", SourceOther},
		{"empty", "", SourceOther},
		{"not a url", "not a url at all", SourceOther},
		{"non-http scheme", "ipfs://bafy.../seg.ts", SourceOther},
		{"protocol-relative is not same-origin", "//cdn.example.com/media/x.ts", SourceOther},

		// A path under the CDN's HOST but not under its configured BASE is not
		// the CDN's — the base carries a path for a reason.
		{"cdn host outside the configured base", "https://cdn.example.com/other/x.ts", SourceOther},

		// The prefix-boundary trap: a bare HasPrefix would attribute an
		// attacker-chosen host's latency to the operator's CDN.
		{"lookalike host", "https://cdn.example.com.attacker.test/media/x.ts", SourceOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.Classify(tc.url); got != tc.want {
				t.Errorf("Classify(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}

// TestClassifyPrefersTheLongerBase: an operator may host the CDN or the gateway
// under a path of the public origin. Matching the public origin first would
// classify every one of those as api-proxy while looking entirely correct, which
// is the failure this ordering exists to prevent.
func TestClassifyPrefersTheLongerBase(t *testing.T) {
	c := NewClassifier(
		"https://vidra.example.com/edge",
		"",
		"",
		"https://vidra.example.com",
	)
	if got := c.Classify("https://vidra.example.com/edge/videos/a/seg.ts"); got != SourceCDN {
		t.Errorf("Classify under the nested CDN base = %q, want %q", got, SourceCDN)
	}
	if got := c.Classify("https://vidra.example.com/api/v1/videos/a/seg.ts"); got != SourceAPIProxy {
		t.Errorf("Classify under the public origin = %q, want %q", got, SourceAPIProxy)
	}
}

// TestClassifyWithNothingConfigured is the default local install: no CDN, no
// gateway, no object store, and often no PUBLIC_BASE_URL either. A relative URL
// still classifies correctly, because a relative fetch cannot have gone anywhere
// but this origin.
func TestClassifyWithNothingConfigured(t *testing.T) {
	c := NewClassifier("", "", "", "")
	if got := c.Classify("/api/v1/videos/a/hls/master.m3u8"); got != SourceAPIProxy {
		t.Errorf("relative URL = %q, want %q", got, SourceAPIProxy)
	}
	if got := c.Classify("https://anything.test/x"); got != SourceOther {
		t.Errorf("absolute URL with nothing configured = %q, want %q", got, SourceOther)
	}
	var nilC *Classifier
	if got := nilC.Classify("https://anything.test/x"); got != SourceOther {
		t.Errorf("nil classifier = %q, want %q", got, SourceOther)
	}
}

// TestClassifyNeverInventsOriginLive: the source is reserved for phase-4 item 7
// and nothing can classify into it today. A live beacon arriving before that
// item lands must not be quietly attributed to some other source's numbers by
// accident, nor produce a value the vocabulary has not accounted for.
func TestClassifyNeverInventsOriginLive(t *testing.T) {
	c := NewClassifier("https://cdn.example.com", "https://gw.example.org", "https://s3.example.net", "https://vidra.example.com")
	for _, u := range []string{"/live/abc/index.m3u8", "https://vidra.example.com/live/abc/seg.ts", "https://elsewhere.test/live/seg.ts"} {
		if got := c.Classify(u); got == SourceOriginLive {
			t.Errorf("Classify(%q) = %q; origin-live is reserved and cannot be derived yet", u, got)
		}
	}
}
