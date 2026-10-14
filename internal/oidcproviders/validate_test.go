package oidcproviders

import (
	"strings"
	"testing"
)

func TestValidators(t *testing.T) {
	for _, tc := range []struct {
		field string
		run   func(*ValidationError)
	}{
		{"name", func(ve *ValidationError) { validateName(ve, "Bad Name") }},
		{"name", func(ve *ValidationError) { validateName(ve, "atproto") }},
		{"name", func(ve *ValidationError) { validateName(ve, strings.Repeat("a", 41)) }},
		{"issuer_url", func(ve *ValidationError) { validateIssuer(ve, "http://sso.example", false) }},
		{"issuer_url", func(ve *ValidationError) { validateIssuer(ve, "https://10.0.0.5", false) }},
		{"issuer_url", func(ve *ValidationError) { validateIssuer(ve, "https://u:p@sso.example", false) }},
		{"issuer_url", func(ve *ValidationError) { validateIssuer(ve, "https://sso.example#x", false) }},
		{"client_id", func(ve *ValidationError) { validateCommon(ve, Input{ClientID: "  "}) }},
		{"scopes", func(ve *ValidationError) { validateCommon(ve, Input{ClientID: "c", Scopes: []string{`a"b`}}) }},
		{"allowed_email_domains", func(ve *ValidationError) {
			validateCommon(ve, Input{ClientID: "c", AllowedEmailDomains: []string{"@corp.example"}})
		}},
	} {
		ve := &ValidationError{}
		tc.run(ve)
		if !ve.Has(tc.field) || len(ve.Fields) != 1 {
			t.Errorf("want exactly one error on %s, got %+v", tc.field, ve.Fields)
		}
	}

	ve := &ValidationError{}
	validateName(ve, "corp-sso")
	validateIssuer(ve, "https://sso.corp.example/realms/main", false)
	validateIssuer(ve, "http://dex:5556/dex", true)
	norm := validateCommon(ve, Input{DisplayName: " Corp ", ClientID: " vidra ",
		Scopes: []string{"email", "groups"}, AllowedEmailDomains: []string{" Corp.Example "}})
	if len(ve.Fields) != 0 {
		t.Fatalf("valid input rejected: %+v", ve.Fields)
	}
	if norm.DisplayName != "Corp" || norm.ClientID != "vidra" || norm.AllowedEmailDomains[0] != "corp.example" {
		t.Errorf("normalized = %+v", norm)
	}
	if got := validateCommon(&ValidationError{}, Input{ClientID: "c"}); got.Scopes == nil || got.AllowedEmailDomains == nil {
		t.Error("normalized lists must be non-nil (the columns are NOT NULL)")
	}
}
