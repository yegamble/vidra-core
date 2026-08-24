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

// A registered provider replaces the built-in taxonomy for validation. Without
// this, an instance whose categories came from a PeerTube import (ids well
// outside the built-in 1..18) rejects every one of them on write.
func TestCategoryProviderReplacesBuiltins(t *testing.T) {
	t.Cleanup(func() { SetCategoryProvider(nil) })

	if !IsCategory("1") || IsCategory("51") {
		t.Fatalf("baseline: built-ins should accept 1 and reject 51")
	}

	SetCategoryProvider(func() []ConfigOption {
		return []ConfigOption{{ID: "51", Label: "Giantess"}, {ID: "65", Label: "Giant (Men)"}}
	})
	if !IsCategory("51") || !IsCategory("65") {
		t.Error("provider ids must validate")
	}
	if IsCategory("1") {
		t.Error("a provider REPLACES the built-ins; stock ids must stop validating")
	}
	if got := CategoryOptions(); len(got) != 2 || got[0].Label != "Giantess" {
		t.Errorf("CategoryOptions() = %+v, want the provider's set with labels", got)
	}

	// An empty provider result falls back rather than leaving no taxonomy at all.
	SetCategoryProvider(func() []ConfigOption { return nil })
	if !IsCategory("1") {
		t.Error("an empty provider must fall back to the built-ins, not reject everything")
	}
}
