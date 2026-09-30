package peertubeimport

import "testing"

func TestImportPolicyMappingFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		source   string
		granular bool
		want     string
		lossy    bool
	}{
		{"do_not_list", false, "hide", false}, {"warn", false, "warn", false}, {"blur", false, "blur", false}, {"display", false, "display", false},
		{"display", true, "hide", true}, {"unexpected-policy", false, "hide", true},
	} {
		t.Run(tc.source, func(t *testing.T) {
			got, lossy := mapSensitivePolicy(SourceUser{NSFWPolicy: &tc.source, GranularNSFW: tc.granular})
			if got == nil || *got != tc.want || lossy != tc.lossy {
				t.Fatalf("mapped=%v lossy=%v", got, lossy)
			}
		})
	}
	if p, lossy := mapSensitivePolicy(SourceUser{}); p != nil || lossy {
		t.Fatal("absent preference became an instruction")
	}
	for _, source := range []int{1, 2, 3, 0, 99} {
		got := mapCommentPolicy(&source)
		want := "disabled"
		if source == 1 {
			want = "enabled"
		}
		if got == nil || *got != want {
			t.Errorf("source policy %d mapped to %v", source, got)
		}
	}
	if mapCommentPolicy(nil) != nil {
		t.Fatal("absent comments policy became an instruction")
	}
}

func TestImportPolicyChangesInvalidateResyncDigest(t *testing.T) {
	hide, warn := "hide", "warn"
	if userPolicyDigest("same-metadata", &hide) == userPolicyDigest("same-metadata", &warn) {
		t.Error("preference-only edit was invisible")
	}
	base := videoPolicyDigest("same-metadata", "enabled", true)
	if base == videoPolicyDigest("same-metadata", "disabled", true) || base == videoPolicyDigest("same-metadata", "enabled", false) {
		t.Error("restriction-only edit was invisible")
	}
}
