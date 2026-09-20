package mail

import (
	"strings"
)

// TransportSettings is every NON-SECRET per-transport field, in one struct so
// the configuration service can hand over a whole document without this package
// knowing where it was stored. Only the block matching the kind is read.
type TransportSettings struct {
	SMTP     SMTPSettings
	Mailgun  MailgunSettings
	Postmark PostmarkSettings
}

// SMTPSettings configures a generic relay. The password is the secret and
// arrives separately.
type SMTPSettings struct {
	Host     string
	Port     int
	Username string
	// Encryption must be explicit for an admin-configured relay:
	// EncryptionAuto is the environment path's compatibility behaviour and is
	// rejected here, because "whatever the relay offers" is a downgrade an
	// operator did not choose.
	Encryption Encryption
}

// MailgunSettings names the sending domain and its region. The API key is the
// secret and arrives separately.
type MailgunSettings struct {
	Domain string
	Region Region
}

// PostmarkSettings carries the message stream. The server token is the secret
// and arrives separately.
type PostmarkSettings struct {
	// MessageStream defaults to "outbound" (Postmark's own default for the
	// transactional stream) when empty.
	MessageStream string
}

// FieldError names one invalid field by the dotted path the admin API uses, so
// a 422 can point at the input that was wrong.
type FieldError struct {
	Field string
	Msg   string
}

// ValidationError is every field problem found in one configuration, so an
// admin fixes the whole form once rather than one field per round trip.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Fields) == 0 {
		return "mail: invalid transport configuration"
	}
	parts := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		parts = append(parts, f.Field+": "+f.Msg)
	}
	return "mail: invalid transport configuration (" + strings.Join(parts, "; ") + ")"
}

// NewTransport builds the transport for one stored configuration: kind picks the
// route, s carries the non-secret fields, and secret is the one credential that
// kind needs (the SMTP password, or the provider's API key / server token). An
// empty secret is valid only for SMTP, where an anonymous relay is a real shape.
//
// Every problem is reported as *ValidationError with dotted field paths, so the
// admin endpoint can answer 422 {fields} without a second validation pass that
// could disagree with this one.
func NewTransport(kind string, s TransportSettings, secret string, opts ...transportOption) (Transport, error) {
	o := collectTransportOptions(opts)
	switch kind {
	case KindSMTP:
		return newValidatedSMTP(s.SMTP, secret, o)
	case KindMailgun:
		return newValidatedMailgun(s.Mailgun, secret, o)
	case KindResend:
		if err := requireSecret("resend.api_key", secret); err != nil {
			return nil, err
		}
		return newResendTransport(secret, o), nil
	case KindBrevo:
		if err := requireSecret("brevo.api_key", secret); err != nil {
			return nil, err
		}
		return newBrevoTransport(secret, o), nil
	case KindPostmark:
		return newValidatedPostmark(s.Postmark, secret, o)
	default:
		return nil, &ValidationError{Fields: []FieldError{{
			Field: "transport",
			Msg:   "unknown transport (want one of " + strings.Join(Kinds(), ", ") + ")",
		}}}
	}
}

func newValidatedSMTP(s SMTPSettings, password string, o transportOptions) (Transport, error) {
	var bad []FieldError
	host := strings.TrimSpace(s.Host)
	switch {
	case host == "":
		bad = append(bad, FieldError{"smtp.host", "required"})
	case hasCRLF(host) || strings.ContainsAny(host, " \t/"):
		bad = append(bad, FieldError{"smtp.host", "must be a bare hostname or IP"})
	}
	if s.Port < 1 || s.Port > 65535 {
		bad = append(bad, FieldError{"smtp.port", "must be between 1 and 65535"})
	}
	switch s.Encryption {
	case EncryptionSTARTTLS, EncryptionTLS, EncryptionNone:
	case EncryptionAuto:
		// Allowed only for the environment path, which does not come through
		// here: an admin who picks "whatever the relay offers" is choosing a
		// silent downgrade to cleartext without knowing it.
		bad = append(bad, FieldError{"smtp.encryption", "must be starttls, tls or none"})
	default:
		bad = append(bad, FieldError{"smtp.encryption", "must be starttls, tls or none"})
	}
	if hasCRLF(s.Username) {
		bad = append(bad, FieldError{"smtp.username", "must not contain line breaks"})
	}
	if hasCRLF(password) {
		bad = append(bad, FieldError{"smtp.password", "must not contain line breaks"})
	}
	if s.Username == "" && password != "" {
		// AUTH PLAIN needs both. A password with no username would be silently
		// dropped, and the operator would read the send as authenticated.
		bad = append(bad, FieldError{"smtp.username", "required when a password is set"})
	}
	if s.Encryption == EncryptionNone && s.Username != "" && !IsLoopbackHost(host) {
		// Refuse the combination HERE, where the message names the real problem.
		// It is already fail-closed at send time — net/smtp's PlainAuth will not
		// transmit credentials over an unencrypted connection unless the server
		// is localhost — but it fails as `auth_failed`, so an operator who
		// pointed `none` + credentials at a LAN relay is told their password is
		// wrong, re-types a password that was correct, and ends up asking for a
		// skip-verify knob that does not exist and will not be added.
		bad = append(bad, FieldError{"smtp.encryption",
			"credentials require starttls or tls unless the relay is on localhost"})
	}
	if len(bad) > 0 {
		return nil, &ValidationError{Fields: bad}
	}
	return newSMTPTransport(smtpTransportConfig{
		Host:       host,
		Port:       s.Port,
		Username:   s.Username,
		Password:   password,
		Encryption: s.Encryption,
		TLSConfig:  o.tlsConfig,
	}), nil
}

func newValidatedMailgun(s MailgunSettings, apiKey string, o transportOptions) (Transport, error) {
	var bad []FieldError
	domain := strings.TrimSpace(s.Domain)
	switch {
	case domain == "":
		bad = append(bad, FieldError{"mailgun.domain", "required"})
	case !isBareDomain(domain):
		// The domain is interpolated into the request PATH. It is an ALLOWLIST,
		// not a denylist of delimiters: a denylist stopped `/` and `..` but let
		// through `%2f` and `%2e%2e`, and Go preserves RawPath, so those bytes
		// reach the wire verbatim and a path-normalising gateway in front of the
		// API could route the call to a different Mailgun endpoint. A sending
		// domain is a hostname; nothing outside a hostname's alphabet belongs.
		bad = append(bad, FieldError{"mailgun.domain", "must be a bare sending domain, e.g. mail.example.org"})
	}
	if s.Region != RegionUS && s.Region != RegionEU {
		bad = append(bad, FieldError{"mailgun.region", "must be us or eu"})
	}
	bad = append(bad, secretProblems("mailgun.api_key", apiKey)...)
	if len(bad) > 0 {
		return nil, &ValidationError{Fields: bad}
	}
	return newMailgunTransport(domain, s.Region, apiKey, o), nil
}

func newValidatedPostmark(s PostmarkSettings, token string, o transportOptions) (Transport, error) {
	bad := secretProblems("postmark.server_token", token)
	stream := strings.TrimSpace(s.MessageStream)
	if stream == "" {
		stream = defaultPostmarkStream
	}
	if hasCRLF(stream) || len(stream) > 100 {
		bad = append(bad, FieldError{"postmark.message_stream", "must be a short stream id, e.g. outbound"})
	}
	if len(bad) > 0 {
		return nil, &ValidationError{Fields: bad}
	}
	return newPostmarkTransport(stream, token, o), nil
}

// SecretField is the dotted path of the ONE credential a kind stores, and the
// name a validation error for it carries. It is exported so the admin endpoint,
// the audit record and the log denylist all name the secret the same way: three
// hand-maintained copies of this list is how a provider key ends up logged in
// the clear after the fourth provider is added.
func SecretField(kind string) string {
	switch kind {
	case KindSMTP:
		return "smtp.password"
	case KindMailgun:
		return "mailgun.api_key"
	case KindResend:
		return "resend.api_key"
	case KindBrevo:
		return "brevo.api_key"
	case KindPostmark:
		return "postmark.server_token"
	}
	return ""
}

// isBareDomain reports whether v is a plain hostname: dot-separated labels of
// letters, digits and hyphens, nothing else. Percent-encoding, path separators,
// dot segments, userinfo and ports are all outside the alphabet and therefore
// all refused by one rule rather than by a list of the tricks known today.
func isBareDomain(v string) bool {
	if v == "" || len(v) > 253 || strings.HasPrefix(v, ".") || strings.HasSuffix(v, ".") ||
		strings.Contains(v, "..") {
		return false
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
		default:
			return false
		}
	}
	return true
}

func requireSecret(field, secret string) error {
	if bad := secretProblems(field, secret); len(bad) > 0 {
		return &ValidationError{Fields: bad}
	}
	return nil
}

// secretProblems checks the one thing a credential must satisfy beyond being
// present: it ends up in an HTTP header, so a line break in it would be header
// injection with an admin-role trigger.
func secretProblems(field, secret string) []FieldError {
	switch {
	case strings.TrimSpace(secret) == "":
		return []FieldError{{field, "required"}}
	case hasCRLF(secret) || strings.ContainsAny(secret, "\x00"):
		return []FieldError{{field, "must not contain line breaks"}}
	}
	return nil
}
