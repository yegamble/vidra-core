package mailconfig

import (
	"encoding/json"
	"sort"
	"strconv"

	"github.com/vidra/vidra-core/internal/mail"
)

// Document is the outbound-mail configuration as an ADMIN reads it: the
// transport, the sender identity, and the non-secret fields of the one block
// that matches the transport. It is never the credential — each block reports
// only whether one is stored, as a *_set boolean.
//
// Only the active transport's block is populated: the document is saved whole,
// and keeping a switched-away provider's half-filled settings around would mean
// the panel could show a Mailgun domain for an instance that sends over Resend.
type Document struct {
	Transport   string         `json:"transport"`
	FromAddress string         `json:"from_address"`
	FromName    string         `json:"from_name"`
	ReplyTo     string         `json:"reply_to"`
	SMTP        *SMTPBlock     `json:"smtp,omitempty"`
	Mailgun     *MailgunBlock  `json:"mailgun,omitempty"`
	Resend      *APIKeyBlock   `json:"resend,omitempty"`
	Brevo       *APIKeyBlock   `json:"brevo,omitempty"`
	Postmark    *PostmarkBlock `json:"postmark,omitempty"`
}

// SMTPBlock is a generic relay's non-secret settings.
type SMTPBlock struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	// Encryption is starttls, tls or none. There is no "auto" here: that is the
	// environment path's compatibility behaviour, and an admin picking
	// "whatever the relay offers" would be choosing a silent downgrade to
	// cleartext without being told.
	Encryption string `json:"encryption"`
	// PasswordSet reports whether a password is stored. The password itself is
	// never returned by any endpoint.
	PasswordSet bool `json:"password_set"`
}

// MailgunBlock names the sending domain and the region it lives in. Region is
// an enum, never a URL — a transport that dialled a hostname out of this
// document would be an SSRF surface with an admin-role trigger.
type MailgunBlock struct {
	Domain    string `json:"domain"`
	Region    string `json:"region"`
	APIKeySet bool   `json:"api_key_set"`
}

// APIKeyBlock is the whole non-secret configuration of a provider whose only
// setting IS the key (Resend, Brevo).
type APIKeyBlock struct {
	APIKeySet bool `json:"api_key_set"`
}

// PostmarkBlock carries the message stream the account sends on.
type PostmarkBlock struct {
	MessageStream  string `json:"message_stream"`
	ServerTokenSet bool   `json:"server_token_set"`
}

// Environment reports what the deployment's env-configured mail path looks
// like, so an admin can see what a DELETE would revert to.
//
// It is deliberately incomplete: host, port and the From address are
// deploy-time facts an admin may need to recognise, and the SMTP USERNAME and
// PASSWORD are on this repository's absolute never-list (see
// httpapi/admin_infra.go). Neither appears here or anywhere else in a response.
type Environment struct {
	Configured bool   `json:"configured"`
	Host       string `json:"host,omitempty"`
	Port       int    `json:"port,omitempty"`
	From       string `json:"from,omitempty"`
}

// Source names where the mail path in force right now comes from.
const (
	// SourceDatabase — the admin-panel document (this package's table).
	SourceDatabase = "database"
	// SourceEnvironment — MAIL_ENABLED + SMTP_* from the deployment's env.
	SourceEnvironment = "environment"
	// SourceDevCapture — DEV_MAIL_CAPTURE_ENABLED. It wins over both of the
	// above: nothing is delivered, messages are held in memory for the local
	// test harness, and saying anything else on the admin page would be a lie
	// to whoever is looking at a developer machine.
	SourceDevCapture = "dev_capture"
	// SourceNone — this instance cannot send email at all.
	SourceNone = "none"
)

// Secret statuses for the stored credential.
const (
	// SecretStatusNone — no credential is stored (no document, or an anonymous
	// relay, which is a legitimate shape).
	SecretStatusNone = "none"
	// SecretStatusOK — the stored credential unsealed.
	SecretStatusOK = "ok"
	// SecretStatusUndecryptable — a credential is stored and this process
	// cannot open it, which means the KEK changed. Deliberately NOT an error
	// exit: the instance is configured-but-broken, every send fails with the
	// typed reason secret_undecryptable, and the admin re-enters the credential.
	// Treating it as "not configured" would silently fall back to the
	// environment transport and send the instance's mail somewhere else.
	SecretStatusUndecryptable = "undecryptable"
)

// State is the whole answer to GET /admin/mail-config.
type State struct {
	Source           string      `json:"source"`
	SecretsAvailable bool        `json:"secrets_available"`
	SecretStatus     string      `json:"secret_status"`
	Environment      Environment `json:"environment"`
	// Config is the stored document, or null when this instance has none.
	Config    *Document `json:"config"`
	UpdatedAt string    `json:"updated_at,omitempty"`
}

// storedSettings is the JSONB payload: the non-secret per-transport fields,
// exactly as they are validated. Only the block matching `transport` is
// meaningful; the rest are omitted so the row never describes two transports.
type storedSettings struct {
	Host          string `json:"host,omitempty"`
	Port          int    `json:"port,omitempty"`
	Username      string `json:"username,omitempty"`
	Encryption    string `json:"encryption,omitempty"`
	Domain        string `json:"domain,omitempty"`
	Region        string `json:"region,omitempty"`
	MessageStream string `json:"message_stream,omitempty"`
}

func (s storedSettings) encode() ([]byte, error) { return json.Marshal(s) }

func decodeSettings(raw []byte) storedSettings {
	var s storedSettings
	if len(raw) > 0 {
		// A row whose settings JSON does not parse is a row written by a future
		// version or by hand. Reading it as empty settings makes the transport
		// build fail loudly at validation rather than panicking here.
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

// transportSettings projects the stored settings onto the shape
// mail.NewTransport validates, so there is exactly one place that knows which
// stored field feeds which transport field.
func (s storedSettings) transportSettings() mail.TransportSettings {
	return mail.TransportSettings{
		SMTP: mail.SMTPSettings{
			Host:       s.Host,
			Port:       s.Port,
			Username:   s.Username,
			Encryption: mail.Encryption(s.Encryption),
		},
		Mailgun:  mail.MailgunSettings{Domain: s.Domain, Region: mail.Region(s.Region)},
		Postmark: mail.PostmarkSettings{MessageStream: s.MessageStream},
	}
}

// document renders the stored row for an admin: the active transport's block
// only, with the credential reported as a boolean.
func (s storedSettings) document(kind, from, fromName, replyTo string, hasSecret bool) *Document {
	d := &Document{Transport: kind, FromAddress: from, FromName: fromName, ReplyTo: replyTo}
	switch kind {
	case mail.KindSMTP:
		d.SMTP = &SMTPBlock{Host: s.Host, Port: s.Port, Username: s.Username, Encryption: s.Encryption, PasswordSet: hasSecret}
	case mail.KindMailgun:
		d.Mailgun = &MailgunBlock{Domain: s.Domain, Region: s.Region, APIKeySet: hasSecret}
	case mail.KindResend:
		d.Resend = &APIKeyBlock{APIKeySet: hasSecret}
	case mail.KindBrevo:
		d.Brevo = &APIKeyBlock{APIKeySet: hasSecret}
	case mail.KindPostmark:
		d.Postmark = &PostmarkBlock{MessageStream: s.MessageStream, ServerTokenSet: hasSecret}
	}
	return d
}

// auditableFields flattens a configuration into the dotted field NAMES and
// their values, so a save can record WHICH fields an admin changed without
// recording what they changed them to. The secret is absent by construction —
// it never enters this map, so no future caller can accidentally diff it into
// an audit row.
func auditableFields(kind, from, fromName, replyTo string, s storedSettings) map[string]string {
	f := map[string]string{
		"transport":    kind,
		"from_address": from,
		"from_name":    fromName,
		"reply_to":     replyTo,
	}
	switch kind {
	case mail.KindSMTP:
		f["smtp.host"] = s.Host
		f["smtp.port"] = strconv.Itoa(s.Port)
		f["smtp.username"] = s.Username
		f["smtp.encryption"] = s.Encryption
	case mail.KindMailgun:
		f["mailgun.domain"] = s.Domain
		f["mailgun.region"] = s.Region
	case mail.KindPostmark:
		f["postmark.message_stream"] = s.MessageStream
	}
	return f
}

// changedFields is the sorted set of dotted names whose value differs between
// two configurations, counting a name present in only one of them as changed.
func changedFields(before, after map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for k, v := range after {
		if before[k] != v {
			seen[k] = true
			out = append(out, k)
		}
	}
	for k, v := range before {
		if !seen[k] && after[k] != v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
