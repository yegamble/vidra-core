// Package mail composes and delivers vidra's transactional email (password
// reset, email verification, operator alerts, the admin test message).
//
// It is split in two halves that meet at one seam:
//
//   - the COMPOSER (composer.go) owns every message vidra sends. It holds the
//     auth.Mailer implementation, decorates subject and body from the instance
//     settings, and hands the finished Message to a transport. There is exactly
//     one of it, so a new delivery route can never fork the message text.
//   - a TRANSPORT (smtp.go and the HTTPS provider files) owns delivery and
//     nothing else: given a Message, put it on the wire, and report failure in
//     terms an operator can act on.
//
// The composer resolves its transport per send from a Resolver, so an admin can
// switch an instance from SMTP to an API provider at runtime without a restart
// and without a second copy of any message.
//
// Security rules (see .ralph/specs/observability.md):
//   - The raw token is a single-use credential. This package never logs — the
//     token exists only in the message body handed to the transport.
//   - SMTP passwords and provider API keys are secrets; they are never logged
//     either (the "smtp_password" key is on the observability sensitive-key
//     denylist, and the provider key names belong there too).
//   - Vendor error bodies are attackable-surface prose: they ride in
//     SendError.Err, truncated, for the LOG. Never put SendError.Err in an HTTP
//     response — SendError.Reason is the part a handler may show.
//   - net/smtp's PlainAuth refuses to send credentials over an unencrypted
//     connection unless the host is localhost, so AUTH never leaks the
//     password to a plaintext network path.
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	netmail "net/mail"
	"strings"
)

// The transport kinds an instance can be configured with. They are the stored
// values, so they are API and are never renamed.
const (
	KindSMTP     = "smtp"
	KindMailgun  = "mailgun"
	KindResend   = "resend"
	KindBrevo    = "brevo"
	KindPostmark = "postmark"
)

// Kinds lists every supported transport. SMTP leads because it is the shape
// every operator already understands and the one the environment path uses;
// the HTTPS providers follow in the order the provider research ranked them for
// a self-hoster on a submission-port-blocking host (Resend, Brevo, Mailgun,
// Postmark).
func Kinds() []string {
	return []string{KindSMTP, KindResend, KindBrevo, KindMailgun, KindPostmark}
}

// Region selects a provider's data region. It is an ENUM, never an
// admin-supplied URL: a transport that dialled a hostname out of the settings
// document would be an SSRF surface with an admin-role trigger.
type Region string

const (
	RegionUS Region = "us"
	RegionEU Region = "eu"
)

// transportOption is the test-only injection seam: an httptest base URL, a stub
// http.Client, a TLS config trusting a self-signed relay. It is unexported so
// no production caller can point a transport at an operator-supplied host — the
// region enum is the only URL choice there is.
type transportOption func(*transportOptions)

type transportOptions struct {
	client    *http.Client
	baseURL   string
	tlsConfig *tls.Config
}

func withHTTPClient(c *http.Client) transportOption {
	return func(o *transportOptions) { o.client = c }
}

func withBaseURL(u string) transportOption {
	return func(o *transportOptions) { o.baseURL = strings.TrimRight(u, "/") }
}

func withTransportTLSConfig(c *tls.Config) transportOption {
	return func(o *transportOptions) { o.tlsConfig = c }
}

func collectTransportOptions(opts []transportOption) transportOptions {
	var o transportOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

// Message is one composed message, ready for any transport. The composer has
// already applied the subject prefix and body signature; a transport renders it
// and delivers it, and changes nothing else.
type Message struct {
	// From is the envelope sender and the From header. Name is optional: when
	// it is empty the header carries the bare address, byte-for-byte what the
	// environment-configured path has always emitted.
	From netmail.Address
	// To is a single recipient address. vidra never sends one message to two
	// people: every message here is addressed to one account, and a shared
	// recipient list would leak one user's address to another.
	To string
	// ReplyTo is optional (the contact form's visitor address).
	ReplyTo string
	Subject string
	// Text is the plain-text body. vidra bodies are plain text by design; a
	// transport whose vendor demands HTML derives it with TextToHTML.
	Text string
}

// Transport delivers composed messages over one route.
type Transport interface {
	// Kind is the stable identifier of the route ("smtp", "mailgun", …), safe
	// to log and to show an admin.
	Kind() string
	// Send delivers one message. It returns *SendError on failure, so a caller
	// can classify without parsing prose.
	Send(ctx context.Context, m Message) error
	// Probe asks "would a send have a chance", and SENDS NOTHING. It runs on
	// the admin status page, so it must never cost the instance a message, a
	// rate-limit budget or a deliverability reputation hit.
	Probe(ctx context.Context) ProbeResult
}

// ProbeResult is what a no-send probe could establish. The three states are
// deliberately distinct:
//
//	Verified=true            — the credentials were proven against the vendor.
//	Verified=false, Err=nil  — the vendor is reachable but the credentials
//	                           cannot be proven WITHOUT sending (a send-only
//	                           API key is the common case). Not a failure:
//	                           reporting it as `down` trains operators to
//	                           ignore the status page.
//	Err != nil               — something is actually wrong. Err is a *SendError
//	                           carrying the Reason (and the SMTP port), so a
//	                           caller can classify it exactly like a send
//	                           failure.
type ProbeResult struct {
	Verified bool
	Err      error
	// Encrypted reports whether the route this probe exercised is protected in
	// transit. Always true for the HTTPS providers. For SMTP it is the fact the
	// handshake observed — and it is a SEPARATE question from Verified, which is
	// the whole point: in EncryptionAuto (the environment path, and therefore
	// every install that predates admin-configurable mail) a relay that stops
	// advertising STARTTLS is silently downgraded to cleartext, the handshake
	// still succeeds, and `smtp: ok` on the admin status page was the only thing
	// anybody would ever see while password-reset tokens crossed the wire in the
	// clear. A relay reconfiguration and a STARTTLS-stripping on-path attacker
	// produce exactly that shape.
	//
	// A relay on loopback reports true: the session never leaves the machine, so
	// there is no cleartext hop to warn about, and warning anyway would train
	// operators to ignore the warning on the one deployment where it is
	// harmless. It is the same line net/smtp's PlainAuth already draws when it
	// decides where credentials may cross an unencrypted connection.
	Encrypted bool
}

// Reason classifies a delivery failure into the small set of things an operator
// can actually DO something about. It is safe to put in an HTTP response; the
// error it came with is not.
type Reason string

const (
	// ReasonAuthFailed — the relay or provider rejected the credentials, or
	// credentials are configured against a relay that offers no AUTH.
	ReasonAuthFailed Reason = "auth_failed"
	// ReasonSenderRejected — the From address or sending domain is not one the
	// provider will send for (unverified domain, unconfirmed sender signature,
	// a Mailgun domain that does not exist in the configured region).
	ReasonSenderRejected Reason = "sender_rejected"
	// ReasonRateLimited — the sending allowance is exhausted (rate limit, daily
	// quota, out of credits).
	ReasonRateLimited Reason = "rate_limited"
	// ReasonProviderUnavailable — the far side failed on its own account (5xx).
	ReasonProviderUnavailable Reason = "provider_unavailable"
	// ReasonTimeout — the conversation ran out of time.
	ReasonTimeout Reason = "timeout"
	// ReasonConnectFailed — the far side could not be reached at all. On SMTP
	// ports 25, 465 and 587 this is the host-blocks-SMTP signature (DigitalOcean
	// blocks all three), which is why SendError carries the port.
	ReasonConnectFailed Reason = "connect_failed"
	// ReasonTLSFailed — the session could not be encrypted as demanded.
	ReasonTLSFailed Reason = "tls_failed"
	// ReasonRejected — refused for some other reason the sender must fix (a
	// rejected recipient, a malformed request).
	ReasonRejected Reason = "rejected"
	// ReasonSecretUndecryptable — the stored credential cannot be unsealed
	// (the KEK changed). Produced by the configuration service rather than a
	// transport, but classified here so every caller reads one vocabulary.
	ReasonSecretUndecryptable Reason = "secret_undecryptable"
)

// SendError is a classified delivery failure.
//
// Err is RAW DETAIL FOR THE LOG: a relay's refusal text or a vendor's error body
// (already truncated by the transport). It may quote attacker-influenced input
// and it may name internal hosts, so it must never reach an HTTP response body.
// Reason and Port are the parts a handler may show.
type SendError struct {
	Reason Reason
	Err    error
	// Port is the SMTP port that failed, 0 for every other transport. It exists
	// so the UI can turn connect_failed on 25/465/587 into the sentence that
	// actually helps: "your host blocks this port; try 2525 or an API provider".
	Port int
}

func (e *SendError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err == nil {
		return "mail: " + string(e.Reason)
	}
	return "mail: " + string(e.Reason) + ": " + e.Err.Error()
}

func (e *SendError) Unwrap() error { return e.Err }

// ReasonOf reports how a send failed. Anything that is not a *SendError is
// ReasonRejected: an unclassified failure is still a failure, and defaulting to
// a benign-sounding reason would hide it.
func ReasonOf(err error) Reason {
	var se *SendError
	if errors.As(err, &se) {
		return se.Reason
	}
	return ReasonRejected
}

// Resolver hands the composer the transport to use for THIS send. It is the
// runtime-configuration seam: the environment path returns a fixed answer
// forever, while the admin-configured path returns whatever the instance is set
// to right now.
//
// ok=false means this instance has no mail path configured. The composer then
// returns nil — the historical noop-mailer contract, which every caller that
// cares about delivery already guards with a capability check.
type Resolver interface {
	Current() (t Transport, from netmail.Address, replyTo string, ok bool)
}

// staticResolver is one fixed transport for the life of the process: the
// environment-configured path, and the shape any test wanting a fixed transport
// should use.
type staticResolver struct {
	transport Transport
	from      netmail.Address
	replyTo   string
}

// NewStaticResolver returns a Resolver that always answers with t. A nil
// transport resolves to "not configured", which is how a composer built over
// nothing stays safe rather than panicking on the first send.
func NewStaticResolver(t Transport, from netmail.Address, replyTo string) Resolver {
	return staticResolver{transport: t, from: from, replyTo: replyTo}
}

func (r staticResolver) Current() (Transport, netmail.Address, string, bool) {
	if r.transport == nil {
		return nil, netmail.Address{}, "", false
	}
	return r.transport, r.from, r.replyTo, true
}

// sanitizeHeader strips CR/LF so a header value can never break the envelope.
func sanitizeHeader(v string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(v)
}

// hasCRLF reports whether v carries a line break — the one character class that
// turns a header value into extra headers. Fields that must be REJECTED rather
// than silently mangled (addresses, credentials, provider identifiers) are
// checked with this; free prose is sanitized instead.
func hasCRLF(v string) bool { return strings.ContainsAny(v, "\r\n") }

// validateMessage refuses a message whose address fields could break out of a
// header. Every transport calls it, so "a CRLF never reaches the wire" is an
// invariant of the Transport interface rather than a property of whichever
// implementation happened to remember — the vendors that accept JSON would
// carry the break harmlessly, right up until it becomes a MIME header at the
// far end.
func validateMessage(m Message) error {
	switch {
	case !IsAddrSpec(m.To):
		return &SendError{Reason: ReasonRejected, Err: errors.New("invalid recipient address")}
	case hasCRLF(m.From.Name) || !IsAddrSpec(m.From.Address):
		return &SendError{Reason: ReasonSenderRejected, Err: errors.New("invalid sender address")}
	case m.ReplyTo != "" && !IsAddrSpec(m.ReplyTo):
		return &SendError{Reason: ReasonRejected, Err: errors.New("invalid reply-to address")}
	}
	return nil
}

// IsAddrSpec reports whether v is exactly ONE bare addr-spec — "user@host" and
// nothing else. It is the ONE address rule in this codebase: internal/mailconfig
// validates an admin's stored identity fields with it, and every transport
// validates the composed message with it, so a configuration that saves is a
// configuration that can be put on the wire.
//
// Three things make an address unusable here and `net/mail.ParseAddress` alone
// catches none of them:
//
//   - A NAME-ADDR parses. `ParseAddress("Vidra <no-reply@example.org>")` and
//     `ParseAddress("<no-reply@example.org>")` both succeed, and these fields go
//     to the wire VERBATIM: `c.Mail(m.From.Address)` emits
//     `MAIL FROM:<Vidra <no-reply@example.org>>` (a 501 from every relay) and
//     Brevo's `sender.email` rejects the same string. The probe QUITs before
//     MAIL FROM, so nothing would surface it: an instance that "saved
//     successfully" would lose every message silently. Requiring Name=="" and
//     Address==v is what makes "parses" mean "is the address".
//   - CRLF turns a header value into extra headers.
//   - The list SEPARATORS are the injection nobody checks for. vidra never sends
//     one message to two people (Message.To says so), but Postmark's To and
//     Mailgun's `to` form field are both documented COMMA-SEPARATED LISTS, so
//     "victim@example.test, attacker@evil.test" in a single string field is a
//     second delivery those two transports would perform and the other three
//     would not.
//
// Address==v is deliberately exact rather than normalising: a value that only
// becomes an addr-spec after the stdlib rewrites it (surrounding whitespace, a
// quoted local part with a space) is not what reaches MAIL FROM, and accepting
// it would reopen the same gap in a quieter form.
func IsAddrSpec(v string) bool {
	if strings.TrimSpace(v) == "" || hasCRLF(v) || strings.ContainsAny(v, ",;") {
		return false
	}
	p, err := netmail.ParseAddress(v)
	return err == nil && p.Name == "" && p.Address == v
}

// formatAddress renders an address for a From header. With no display name it
// emits the bare address — exactly what this package has always written, so the
// environment path's messages are unchanged to the byte. With a display name it
// goes through the stdlib, which quotes and RFC 2047-encodes as needed.
func formatAddress(a netmail.Address) string {
	if strings.TrimSpace(a.Name) == "" {
		return a.Address
	}
	return a.String()
}
