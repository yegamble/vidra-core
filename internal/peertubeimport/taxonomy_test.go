package peertubeimport

import (
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/video"
)

// The real shape, as read off a live source: the settings column is JSON, and
// the taxonomy lives in it as a STRING that has to be parsed as JSON a SECOND
// time. Getting this wrong reads as "this instance has no custom taxonomy",
// which is silent — the import would carry category ids that validate against
// nothing and nobody would see an error.
const liveSettingsBlob = `{
	"json-categories-as-text": "{\"add\":[{\"key\":51,\"label\":\"Giantess\"},{\"key\":52,\"label\":\"Shrunken\"}],\"delete\":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18]}"
}`

func TestParsePluginCategoriesDoubleEncoded(t *testing.T) {
	tax, ok := parsePluginCategories(liveSettingsBlob)
	if !ok {
		t.Fatal("the plugin settings were not recognised as a taxonomy")
	}
	if len(tax.Add) != 2 || tax.Add[0] != (SourceCategory{ID: 51, Label: "Giantess"}) || tax.Add[1].ID != 52 {
		t.Fatalf("add = %+v, want the two custom categories", tax.Add)
	}
	if len(tax.Delete) != 18 || tax.Delete[0] != 1 || tax.Delete[17] != 18 {
		t.Fatalf("delete = %v, want all eighteen stock ids", tax.Delete)
	}
}

func TestParsePluginCategoriesAcceptsANestedObject(t *testing.T) {
	// Not the shape the plugin writes, but insisting on the double encoding would
	// be insisting on an implementation detail rather than on the data.
	tax, ok := parsePluginCategories(`{"json-categories-as-text":{"add":[{"key":"77","label":"Docs"}],"delete":[]}}`)
	if !ok {
		t.Fatal("a nested object was not read")
	}
	if len(tax.Add) != 1 || tax.Add[0].ID != 77 {
		t.Fatalf("add = %+v, want id 77 read from its string form", tax.Add)
	}
}

func TestParsePluginCategoriesRejectsNonTaxonomies(t *testing.T) {
	cases := map[string]string{
		"not json":            `not json at all`,
		"no taxonomy key":     `{"some-other-setting": true}`,
		"empty string value":  `{"json-categories-as-text": ""}`,
		"says nothing":        `{"json-categories-as-text": "{\"add\":[],\"delete\":[]}"}`,
		"unparseable payload": `{"json-categories-as-text": "{not json}"}`,
	}
	for name, blob := range cases {
		if _, ok := parsePluginCategories(blob); ok {
			t.Errorf("%s: read as a taxonomy, want no taxonomy (the built-in list must stand)", name)
		}
	}
}

func TestFoldCategoriesAddAndDelete(t *testing.T) {
	tax, ok := parsePluginCategories(liveSettingsBlob)
	if !ok {
		t.Fatal("parse failed")
	}
	got := foldCategories(video.Categories, tax)
	want := []video.ConfigOption{{ID: "51", Label: "Giantess"}, {ID: "52", Label: "Shrunken"}}
	if !sameCategories(got, want) {
		t.Fatalf("effective taxonomy = %+v, want only the instance's own two", got)
	}
}

func TestFoldCategoriesKeepsUndeletedBuiltinsInNumericOrder(t *testing.T) {
	// Only two stock ids withdrawn, one custom id added. The survivors stay, and
	// the order is NUMERIC — "10" sorting before "2" would be a taxonomy in a
	// nonsense order.
	got := foldCategories(video.Categories, SourceCategoryTaxonomy{
		Add:    []SourceCategory{{ID: 51, Label: "Giantess"}},
		Delete: []int{1, 18},
	})
	if len(got) != 17 {
		t.Fatalf("kept %d categories, want 17 (18 built-ins less 2 deleted plus 1 added)", len(got))
	}
	if got[0].ID != "2" || got[8].ID != "10" || got[len(got)-1].ID != "51" {
		t.Fatalf("order = %v, want ascending numeric ids ending at the custom 51", ids(got))
	}
}

func TestFoldCategoriesAddRenamesAStockID(t *testing.T) {
	got := foldCategories(video.Categories, SourceCategoryTaxonomy{
		Add: []SourceCategory{{ID: 1, Label: "Musique"}},
	})
	if got[0] != (video.ConfigOption{ID: "1", Label: "Musique"}) {
		t.Fatalf("category 1 = %+v, want the instance's own label for it", got[0])
	}
	if len(got) != len(video.Categories) {
		t.Fatalf("kept %d, want the built-in count unchanged", len(got))
	}
}

func TestFoldCategoriesDropsWhatCannotBeStored(t *testing.T) {
	got := foldCategories(nil, SourceCategoryTaxonomy{
		Add: []SourceCategory{
			{ID: 0, Label: "unreadable id"},  // flexInt could not read the key
			{ID: -3, Label: "negative"},      // the setting's ids are digits only
			{ID: 60, Label: "   "},           // a category with no name is not a category
			{ID: 61, Label: " Two\nWords\t"}, // one line, trimmed
			{ID: 62, Label: strings.Repeat("x", maxCategoryLabel+40)},
		},
	})
	if len(got) != 2 {
		t.Fatalf("kept %+v, want only the two storable entries", got)
	}
	if got[0].Label != "Two Words" {
		t.Errorf("label = %q, want the collapsed one-line form", got[0].Label)
	}
	if n := len([]rune(got[1].Label)); n != maxCategoryLabel {
		t.Errorf("label length = %d runes, want it bounded at %d", n, maxCategoryLabel)
	}
	// Whatever survives has to be storable in the setting itself.
	value := instancesettings.FormatList(categoryEntries(got))
	if err := instancesettings.Validate(instancesettings.KeyInstanceCustomCategories, value); err != nil {
		t.Errorf("the folded taxonomy does not validate as a setting: %v", err)
	}
}

func TestCarriedTaxonomyValidatesAndReadsBack(t *testing.T) {
	tax, _ := parsePluginCategories(liveSettingsBlob)
	value := instancesettings.FormatList(categoryEntries(foldCategories(video.Categories, tax)))
	if err := instancesettings.Validate(instancesettings.KeyInstanceCustomCategories, value); err != nil {
		t.Fatalf("carried taxonomy rejected by the setting it is written to: %v", err)
	}
	if want := `["51:Giantess","52:Shrunken"]`; value != want {
		t.Fatalf("stored value = %s, want %s", value, want)
	}
}

// The re-run matrix. The operator runs this import on a SCHEDULE up to cutover,
// so every one of these happens repeatedly against a target that may have been
// edited in between.
func TestDecideTaxonomyRerunMatrix(t *testing.T) {
	const source = `["51:Giantess"]`
	const moved = `["51:Giantess","52:Shrunken"]`
	cases := []struct {
		name    string
		desired string
		target  taxonomyTarget
		want    taxonomyAction
	}{
		{
			name:    "first import writes",
			desired: source,
			target:  taxonomyTarget{},
			want:    taxonomyWrite,
		},
		{
			name:    "second run writes nothing",
			desired: source,
			target:  taxonomyTarget{current: source, hasOverride: true, applied: source},
			want:    taxonomyUpToDate,
		},
		{
			name:    "the source gained a category, and the value is still the import's own",
			desired: moved,
			target:  taxonomyTarget{current: source, hasOverride: true, applied: source},
			want:    taxonomyWrite,
		},
		{
			name:    "an operator edited the taxonomy: never clobbered",
			desired: moved,
			target:  taxonomyTarget{current: `["51:Giantesses (edited)"]`, hasOverride: true, applied: source},
			want:    taxonomyOperatorOwned,
		},
		{
			name:    "a taxonomy configured by hand before any import ran",
			desired: source,
			target:  taxonomyTarget{current: `["90:Handmade"]`, hasOverride: true},
			want:    taxonomyOperatorOwned,
		},
		{
			name:    "an operator cleared the key back to the built-ins",
			desired: source,
			target:  taxonomyTarget{applied: source},
			want:    taxonomyCleared,
		},
		{
			name:    "an operator typed exactly what the source says",
			desired: source,
			target:  taxonomyTarget{current: source, hasOverride: true},
			want:    taxonomyUpToDate,
		},
	}
	for _, tc := range cases {
		if got := decideTaxonomy(tc.desired, tc.target); got != tc.want {
			t.Errorf("%s: decided %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A source whose plugin describes exactly the built-in list gets NO override:
// restating the shipped list as an override freezes it against every future
// change to it, and buys nothing today.
func TestBuiltinEquivalentTaxonomyNeedsNoOverride(t *testing.T) {
	var add []SourceCategory
	for _, o := range video.Categories {
		add = append(add, SourceCategory{ID: mustAtoi(t, o.ID), Label: o.Label})
	}
	got := foldCategories(video.Categories, SourceCategoryTaxonomy{Add: add})
	if !sameCategories(got, video.Categories) {
		t.Fatalf("folded = %v, want the built-in list exactly", ids(got))
	}
}

func TestSameCategories(t *testing.T) {
	a := []video.ConfigOption{{ID: "1", Label: "Music"}}
	if !sameCategories(a, []video.ConfigOption{{ID: "1", Label: "Music"}}) {
		t.Error("identical taxonomies compared unequal")
	}
	if sameCategories(a, []video.ConfigOption{{ID: "1", Label: "Musique"}}) {
		t.Error("a relabelled category compared equal — a rename would never be carried")
	}
	if sameCategories(a, nil) {
		t.Error("an empty taxonomy compared equal to a populated one")
	}
}

func ids(opts []video.ConfigOption) []string {
	out := make([]string, 0, len(opts))
	for _, o := range opts {
		out = append(out, o.ID)
	}
	return out
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	var n int
	for _, r := range s {
		if r < '0' || r > '9' {
			t.Fatalf("built-in category id %q is not numeric", s)
		}
		n = n*10 + int(r-'0')
	}
	return n
}
