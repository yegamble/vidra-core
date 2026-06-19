package httpapi

import "testing"

func TestBearerToken(t *testing.T) {
	cases := []struct {
		header  string
		wantTok string
		wantOK  bool
		named   string
	}{
		{"Bearer abc.def.ghi", "abc.def.ghi", true, "standard"},
		{"bearer abc", "abc", true, "lowercase scheme"},
		{"BEARER abc", "abc", true, "uppercase scheme"},
		{"Bearer    spaced  ", "spaced", true, "trimmed"},
		{"", "", false, "empty"},
		{"Bearer ", "", false, "scheme only"},
		{"Bearer", "", false, "no space"},
		{"Basic abc", "", false, "wrong scheme"},
		{"abc.def.ghi", "", false, "no scheme"},
	}
	for _, tc := range cases {
		gotTok, gotOK := bearerToken(tc.header)
		if gotOK != tc.wantOK || gotTok != tc.wantTok {
			t.Errorf("%s: bearerToken(%q) = (%q, %v), want (%q, %v)",
				tc.named, tc.header, gotTok, gotOK, tc.wantTok, tc.wantOK)
		}
	}
}
