package oidcproviders

import (
	"regexp"
	"strings"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/urlsafety"
)

// Input is a create/update body. ClientSecret is write-only: absent keeps the
// stored one (update only), a value replaces it. Enabled/AutoProvision absent
// mean true on create and "unchanged" on update. IssuerURL is fixed at create;
// on update it may be omitted or repeated unchanged.
type Input struct {
	DisplayName         string   `json:"display_name"`
	IssuerURL           string   `json:"issuer_url"`
	ClientID            string   `json:"client_id"`
	ClientSecret        *string  `json:"client_secret"`
	Scopes              []string `json:"scopes"`
	Enabled             *bool    `json:"enabled"`
	AutoProvision       *bool    `json:"auto_provision"`
	AllowedEmailDomains []string `json:"allowed_email_domains"`
}

// nameRE matches config.oauthProviderName: URL-path and env-var safe.
var nameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// domainRE is a lowercase DNS name with at least one dot.
var domainRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// scopeRE is an RFC 6749 scope-token (no space, quote or backslash).
var scopeRE = regexp.MustCompile(`^[\x21\x23-\x5b\x5d-\x7e]{1,64}$`)

// FieldError names one rejected field by its JSON name.
type FieldError struct {
	Field string
	Msg   string
}

// ValidationError lists every rejected field of one request.
type ValidationError struct{ Fields []FieldError }

func (e *ValidationError) Error() string {
	names := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		names = append(names, f.Field)
	}
	return "oidcproviders: invalid " + strings.Join(names, ",")
}

func (e *ValidationError) add(field, msg string) { e.Fields = append(e.Fields, FieldError{field, msg}) }

// Has reports whether field was rejected.
func (e *ValidationError) Has(field string) bool {
	for _, f := range e.Fields {
		if f.Field == field {
			return true
		}
	}
	return false
}

// validateName checks a new provider's name: URL-path and env-var safe, and
// not "atproto", which ATProto sign-in already writes into oauth_identities.
func validateName(ve *ValidationError, name string) {
	if !nameRE.MatchString(name) || len(name) > 40 || name == auth.ATProtoProviderName {
		ve.add("name", "must be lowercase letters, digits and single dashes (max 40), and not a reserved name")
	}
}

// validateCommon checks the fields create and update share, returning them
// normalized (trimmed, domains lowercased, never-nil lists).
func validateCommon(ve *ValidationError, in Input) Input {
	out := Input{DisplayName: strings.TrimSpace(in.DisplayName), ClientID: strings.TrimSpace(in.ClientID),
		Scopes: []string{}, AllowedEmailDomains: []string{}}
	if len(out.DisplayName) > 64 {
		ve.add("display_name", "must be at most 64 characters")
	}
	if out.ClientID == "" || len(out.ClientID) > 255 {
		ve.add("client_id", "is required (at most 255 characters)")
	}
	if in.ClientSecret != nil && len(*in.ClientSecret) > 1024 {
		ve.add("client_secret", "must be at most 1024 characters")
	}
	if len(in.Scopes) > 20 {
		ve.add("scopes", "at most 20 scopes")
	}
	for _, sc := range in.Scopes {
		if !scopeRE.MatchString(sc) {
			ve.add("scopes", "each scope must be a single token without spaces or quotes")
			break
		}
		out.Scopes = append(out.Scopes, sc)
	}
	if len(in.AllowedEmailDomains) > 50 {
		ve.add("allowed_email_domains", "at most 50 domains")
	}
	for _, d := range in.AllowedEmailDomains {
		d = strings.ToLower(strings.TrimSpace(d))
		if len(d) > 253 || !domainRE.MatchString(d) {
			ve.add("allowed_email_domains", "each entry must be a domain name such as example.com")
			break
		}
		out.AllowedEmailDomains = append(out.AllowedEmailDomains, d)
	}
	return out
}

func validateIssuer(ve *ValidationError, raw string, allowPrivate bool) {
	u, err := urlsafety.Guard{AllowPrivate: allowPrivate}.ValidateURL(raw)
	switch {
	case err != nil:
		ve.add("issuer_url", "must be an absolute URL with a public host")
	case u.Scheme != "https" && !allowPrivate:
		ve.add("issuer_url", "must use https")
	case u.RawQuery != "" || u.Fragment != "":
		ve.add("issuer_url", "must not carry a query or fragment")
	}
}
