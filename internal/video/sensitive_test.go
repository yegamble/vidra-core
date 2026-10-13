package video

import "testing"

// Policy resolution: flags x viewer overrides x base. The most restrictive
// applicable policy wins; SQL vidra_sensitive_hidden() must agree on "hide"
// (proven against Postgres in internal/store).
func TestResolveSensitivePolicy(t *testing.T) {
	cases := []struct {
		name      string
		base      string
		overrides map[string]string
		flags     []string
		sensitive bool
		want      string
	}{
		{"not sensitive ignores everything", "hide", map[string]string{"violent": "hide"}, []string{"violent"}, false, "display"},
		{"unflagged sensitive follows base", "blur", nil, nil, true, "blur"},
		{"unflagged ignores overrides", "blur", map[string]string{"violent": "hide"}, nil, true, "blur"},
		{"flag without override follows base", "warn", nil, []string{"sex"}, true, "warn"},
		{"override loosens base", "hide", map[string]string{"violent": "display"}, []string{"violent"}, true, "display"},
		{"override tightens base", "display", map[string]string{"sex": "hide"}, []string{"sex"}, true, "hide"},
		{"most restrictive of several flags", "display", map[string]string{"violent": "blur", "sex": "warn"}, []string{"violent", "sex"}, true, "warn"},
		{"one inheriting flag keeps base in the mix", "hide", map[string]string{"violent": "display"}, []string{"violent", "shocking"}, true, "hide"},
		{"override for a flag the video lacks is inert", "display", map[string]string{"sex": "hide"}, []string{"violent"}, true, "display"},
		{"invalid override value is ignored", "warn", map[string]string{"sex": "nuke"}, []string{"sex"}, true, "warn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveSensitivePolicy(tc.base, tc.overrides, tc.flags, tc.sensitive); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeSensitiveFlags(t *testing.T) {
	got := NormalizeSensitiveFlags([]string{"sex", "bogus", "violent", "sex"})
	if len(got) != 2 || got[0] != "violent" || got[1] != "sex" {
		t.Errorf("got %v, want [violent sex]", got)
	}
	if NormalizeSensitiveFlags(nil) == nil {
		t.Error("must be non-nil: the column is NOT NULL")
	}
}
