package catalog

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestGenerationWireUsesCanonicalPositiveInt64Strings(t *testing.T) {
	for _, wire := range []string{`"1"`, `"9007199254740993"`, `"9223372036854775807"`} {
		var got Generation
		if err := json.Unmarshal([]byte(wire), &got); err != nil {
			t.Fatal(err)
		}
		out, err := json.Marshal(got)
		if err != nil || string(out) != wire {
			t.Fatalf("round trip %s = %s, %v", wire, out, err)
		}
	}
	for _, wire := range []string{`1`, `null`, `""`, `"0"`, `"-1"`, `"+1"`, `"01"`, `"1.0"`, `" 1"`, `"9223372036854775808"`} {
		var got Generation
		if err := json.Unmarshal([]byte(wire), &got); err == nil {
			t.Errorf("accepted invalid generation %s", wire)
		}
	}
	for _, invalid := range []Generation{0, -1} {
		if _, err := json.Marshal(invalid); err == nil {
			t.Error("encoded an unallocated generation")
		}
	}
}

func TestObservationIdentityRejectsUnownedScopeAndMissingFence(t *testing.T) {
	valid := Identity{Scope: ScopeLocal, Epoch: uuid.New(), Generation: 1}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Identity){
		func(v *Identity) { v.Scope = "remote" },
		func(v *Identity) { v.Scope = "" },
		func(v *Identity) { v.Epoch = uuid.Nil },
		func(v *Identity) { v.Generation = 0 },
		func(v *Identity) { v.Generation = -1 },
	} {
		bad := valid
		mutate(&bad)
		if err := bad.Validate(); err == nil {
			t.Errorf("accepted invalid identity %+v", bad)
		}
	}
}

func TestManifestValidatesCountsAndCanonicalDigests(t *testing.T) {
	const empty = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	for _, valid := range []Manifest{
		{MembershipSHA256: empty, PagesSHA256: empty},
		{PageCount: 1, DocumentCount: 2, MembershipSHA256: strings.Repeat("a", 64), PagesSHA256: strings.Repeat("b", 64)},
	} {
		if err := valid.Validate(); err != nil {
			t.Fatalf("valid manifest %+v: %v", valid, err)
		}
	}
	for _, invalid := range []Manifest{
		{},
		{PageCount: -1, MembershipSHA256: empty, PagesSHA256: empty},
		{DocumentCount: -1, MembershipSHA256: empty, PagesSHA256: empty},
		{PageCount: 2, DocumentCount: 1, MembershipSHA256: empty, PagesSHA256: empty},
		{PageCount: 1, DocumentCount: 0, MembershipSHA256: empty, PagesSHA256: empty},
		{PageCount: 0, DocumentCount: 1, MembershipSHA256: empty, PagesSHA256: empty},
		{PageCount: 1, DocumentCount: 1, MembershipSHA256: strings.ToUpper(empty), PagesSHA256: empty},
		{MembershipSHA256: strings.Repeat("a", 64), PagesSHA256: empty},
		{MembershipSHA256: empty, PagesSHA256: "invalid"},
	} {
		if err := invalid.Validate(); err == nil {
			t.Errorf("accepted invalid manifest %+v", invalid)
		}
	}
}
