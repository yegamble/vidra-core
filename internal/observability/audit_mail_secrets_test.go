package observability

import (
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/mail"
)

// TestMailSecretFieldsAreOnTheDenylist drives the sensitive-key list from
// mail.SecretField rather than from a hand-copied list of provider names.
//
// WHY IT IS WRITTEN THIS WAY. The denylist carried "smtp_password" for years,
// which was complete while SMTP was the only transport there was. Four HTTPS
// providers arrive with four more credentials, and the failure mode of adding
// the fifth provider is not a broken build — it is a provider API key written
// into a structured log in the clear, fanned out to an aggregator with a wider
// audience and a longer memory than the database column ever had, and noticed
// years later if at all.
//
// So the test enumerates mail.Kinds(). Add a transport without adding its
// credential here and this goes red, naming the key that is missing.
//
// Both spellings are required. The dotted path is what the admin API, the 422
// field errors and the audit metadata all call the field; the LEAF is what a
// careless structured-log call would use ("api_key", 5, ...). IsSensitiveKey is
// exact-match and case-insensitive, so neither spelling covers the other.
func TestMailSecretFieldsAreOnTheDenylist(t *testing.T) {
	for _, kind := range mail.Kinds() {
		field := mail.SecretField(kind)
		if field == "" {
			t.Fatalf("mail.SecretField(%q) is empty — every transport stores exactly one credential", kind)
		}
		if !IsSensitiveKey(field) {
			t.Errorf("the %s credential is named %q by the admin API and that key is NOT on the log denylist; add it to sensitiveKeys", kind, field)
		}
		// The leaf name ("password", "api_key", "server_token") and the
		// underscored provider spelling ("mailgun_api_key") are the two ways the
		// same secret reaches a log line by accident.
		leaf := field
		if i := strings.LastIndex(field, "."); i >= 0 {
			leaf = field[i+1:]
		}
		if !IsSensitiveKey(leaf) {
			t.Errorf("the %s credential's leaf name %q is not on the log denylist", kind, leaf)
		}
		if underscored := strings.ReplaceAll(field, ".", "_"); !IsSensitiveKey(underscored) {
			t.Errorf("the %s credential's underscored spelling %q is not on the log denylist", kind, underscored)
		}
	}
}
