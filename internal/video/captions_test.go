package video

import "testing"

func TestIsWebVTT(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"exact", "WEBVTT", true},
		{"newline", "WEBVTT\n\n00:00.000 --> 00:01.000\nhi", true},
		{"crlf", "WEBVTT\r\n", true},
		{"space header", "WEBVTT - some header\n", true},
		{"tab header", "WEBVTT\tx", true},
		{"utf8 bom", "\xEF\xBB\xBFWEBVTT\n", true},
		{"no signature", "not a caption", false},
		{"glued suffix", "WEBVTTX", false},
		{"empty", "", false},
		{"lowercase", "webvtt\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isWebVTT([]byte(tc.in)); got != tc.want {
				t.Errorf("isWebVTT(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestLangPattern(t *testing.T) {
	valid := []string{"en", "es", "pt-BR", "zh-Hans", "en-US-x-1"}
	invalid := []string{"", "e", "1", "en_US", "not a lang!", "toolonglang"}
	for _, v := range valid {
		if !langPattern.MatchString(v) {
			t.Errorf("langPattern rejected valid %q", v)
		}
	}
	for _, v := range invalid {
		if langPattern.MatchString(v) {
			t.Errorf("langPattern accepted invalid %q", v)
		}
	}
}
