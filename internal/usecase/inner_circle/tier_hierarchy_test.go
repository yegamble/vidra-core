package inner_circle

import "testing"

func TestTierRank(t *testing.T) {
	cases := []struct {
		tier string
		want int
	}{
		{"", 0},
		{"none", 0},
		{"unknown", 0},
		{"supporter", 1},
		{"vip", 2},
		{"elite", 3},
	}
	for _, tc := range cases {
		t.Run(tc.tier, func(t *testing.T) {
			if got := TierRank(tc.tier); got != tc.want {
				t.Fatalf("TierRank(%q) = %d, want %d", tc.tier, got, tc.want)
			}
		})
	}
}

func TestHasAccess_Matrix(t *testing.T) {
	tiers := []string{"", "supporter", "vip", "elite"}
	for _, member := range tiers {
		for _, required := range tiers {
			t.Run(member+"_can_access_"+required, func(t *testing.T) {
				want := TierRank(member) >= TierRank(required)
				if got := HasAccess(member, required); got != want {
					t.Fatalf("HasAccess(%q, %q) = %v, want %v", member, required, got, want)
				}
			})
		}
	}
}

func TestHasAccess_RequiredEmptyMeansPublic(t *testing.T) {
	if !HasAccess("", "") {
		t.Fatalf("public content should be accessible to non-members")
	}
	if !HasAccess("supporter", "") {
		t.Fatalf("public content should be accessible to members")
	}
}

func TestHasAccess_NonMemberCannotAccessGated(t *testing.T) {
	if HasAccess("", "supporter") {
		t.Fatalf("non-member must not access supporter content")
	}
	if HasAccess("", "elite") {
		t.Fatalf("non-member must not access elite content")
	}
}

func TestHasAccess_HigherTierAccessesLower(t *testing.T) {
	if !HasAccess("elite", "supporter") {
		t.Fatalf("elite must access supporter content")
	}
	if !HasAccess("elite", "vip") {
		t.Fatalf("elite must access vip content")
	}
	if !HasAccess("vip", "supporter") {
		t.Fatalf("vip must access supporter content")
	}
}

func TestHasAccess_LowerTierBlocked(t *testing.T) {
	if HasAccess("supporter", "vip") {
		t.Fatalf("supporter must not access vip content")
	}
	if HasAccess("supporter", "elite") {
		t.Fatalf("supporter must not access elite content")
	}
	if HasAccess("vip", "elite") {
		t.Fatalf("vip must not access elite content")
	}
}

func TestValidTierID(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"supporter", true},
		{"vip", true},
		{"elite", true},
		{"", false},
		{"none", false},
		{"Elite", false},
		{"diamond", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := ValidTierID(tc.in); got != tc.want {
				t.Fatalf("ValidTierID(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
