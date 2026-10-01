package instancesettings

import (
	"strings"
	"testing"
)

// secretShapedReason / fetchedURLReason classify a registry key by NAME alone.
// They return "" when the key is clean, else the rule that fired.
//
// WHY a name-pattern gate: the overlay is editable by any admin through the UI
// and is stored in the database, in plaintext, where backups, exports and the
// audit trail can reach it. A credential would be readable by every admin and
// leak with every dump, and a server-fetched URL would let an admin point the
// server at an arbitrary host (SSRF via the settings UI). Both stay env-only.
// Matching is on `_`-separated tokens (never raw substrings) so "security" or
// "monkey" cannot trip a "uri"/"key" rule by accident.
func secretShapedReason(key string) string {
	toks := strings.Split(strings.ToLower(key), "_")
	has := func(want ...string) bool {
		for _, t := range toks {
			for _, w := range want {
				if t == w {
					return true
				}
			}
		}
		return false
	}
	switch {
	case has("secret", "secrets", "kek", "password", "passwords", "passwd", "token", "tokens", "credential", "credentials"):
		return "secret-shaped token"
	case strings.Contains(strings.ToLower(key), "private_key"),
		strings.Contains(strings.ToLower(key), "api_key"),
		strings.Contains(strings.ToLower(key), "access_key"),
		strings.Contains(strings.ToLower(key), "secret_key"):
		return "key-material phrase"
	case toks[len(toks)-1] == "key":
		return "name ends in _key"
	}
	return ""
}

func fetchedURLReason(key string) string {
	toks := strings.Split(strings.ToLower(key), "_")
	switch last := toks[len(toks)-1]; last {
	case "url", "urls", "endpoint", "host", "webhook":
		return "name ends in _" + last
	}
	for _, t := range toks {
		if t == "uri" || t == "uris" {
			return "name contains uri"
		}
	}
	return ""
}

// registryKeyAllowlist lists existing keys that match a rule by name but are
// safe. Each entry carries the reason; adding one is a reviewed decision. An
// entry that no longer matches any rule (or is no longer in the registry)
// fails the test, so the list cannot rot into a blanket exemption.
var registryKeyAllowlist = map[string]string{
	// Verified: terms_url / privacy_url are only validated (validateOptionalURL)
	// and returned in the public instance payload; no server-side code fetches them.
	KeyTermsURL:   "rendered by the client, never fetched server-side",
	KeyPrivacyURL: "rendered by the client, never fetched server-side",
	// Verified: both are bool toggles gating search_remote.go; the value is not
	// a URL. The URI that gets fetched comes from the user's search query and goes
	// through the SSRF-guarded federation resolver, so the admin cannot aim it.
	KeySearchRemoteURIUsers:     "bool toggle, not a URL; fetch target comes from the query via the SSRF-guarded resolver",
	KeySearchRemoteURIAnonymous: "bool toggle, not a URL; fetch target comes from the query via the SSRF-guarded resolver",
}

func registryKeyViolation(key string) string {
	if r := secretShapedReason(key); r != "" {
		return r
	}
	return fetchedURLReason(key)
}

// TestRegistryKeyMatcher proves the matcher fires, so a green run of the real
// registry assertion below means something.
func TestRegistryKeyMatcher(t *testing.T) {
	bad := []string{
		"smtp_password", "SMTP_PASSWD", "jwt_secret", "federation_kek", "api_token",
		"s3_secret_key", "s3_access_key", "stripe_api_key", "tls_private_key",
		"db_credentials", "webhook_signing_secret", "signing_key",
		"search_service_url", "s3_endpoint", "smtp_host", "alert_webhook", "remote_uri",
		"Logo_URL",
	}
	for _, k := range bad {
		if registryKeyViolation(k) == "" {
			t.Errorf("matcher missed %q", k)
		}
	}
	// Clean names, including look-alikes a substring matcher would flag.
	good := []string{
		"instance_name", "upload_max_size_bytes", "security_policy", "monkey_mode",
		"keyboard_shortcuts_enabled", "tokenizer_mode", "default_feed_sort",
	}
	for _, k := range good {
		if r := registryKeyViolation(k); r != "" {
			t.Errorf("matcher false positive on %q: %s", k, r)
		}
	}
}

// TestRegistryHoldsNoSecretsOrFetchedURLs: settings registry must never hold
// secrets or server-fetched URLs; see council D1.
func TestRegistryHoldsNoSecretsOrFetchedURLs(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range specs {
		if reason, ok := registryKeyAllowlist[s.key]; ok {
			if registryKeyViolation(s.key) == "" {
				t.Errorf("allowlist entry %q (%s) matches no rule; remove it", s.key, reason)
			}
			seen[s.key] = true
			continue
		}
		if r := registryKeyViolation(s.key); r != "" {
			t.Errorf("registry key %q is %s: the settings registry must never hold secrets or server-fetched URLs; see council D1. Secrets and server-fetched URLs stay env-only (an admin-editable DB value would leak via dumps or enable SSRF)", s.key, r)
		}
	}
	for k := range registryKeyAllowlist {
		if !seen[k] {
			t.Errorf("allowlist entry %q is not in the registry; remove it", k)
		}
	}
}
