package video

import "testing"

func TestConfigOptionsAreWellFormed(t *testing.T) {
	lists := map[string][]ConfigOption{
		"categories": Categories,
		"licenses":   Licenses,
		"languages":  Languages,
		"privacies":  Privacies,
	}
	for name, opts := range lists {
		if len(opts) == 0 {
			t.Errorf("%s: expected a non-empty list", name)
		}
		seen := map[string]bool{}
		for _, o := range opts {
			if o.ID == "" || o.Label == "" {
				t.Errorf("%s: option has empty id/label: %+v", name, o)
			}
			if seen[o.ID] {
				t.Errorf("%s: duplicate id %q", name, o.ID)
			}
			seen[o.ID] = true
		}
	}
}

func TestConfigValidators(t *testing.T) {
	if !IsCategory("1") || IsCategory("999") || IsCategory("") {
		t.Error("IsCategory: expected 1 valid, 999/empty invalid")
	}
	if !IsLicense("7") || IsLicense("8") {
		t.Error("IsLicense: expected 7 valid, 8 invalid")
	}
	if !IsLanguage("en") || IsLanguage("xx") {
		t.Error("IsLanguage: expected en valid, xx invalid")
	}
	// Privacies deliberately use the video privacy values as ids.
	if Privacies[0].ID != "public" {
		t.Errorf("first privacy id = %q; want public", Privacies[0].ID)
	}
}
