package preflight

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
)

// SMTPHandshake checks the steps a real send actually depends on — EHLO, then
// STARTTLS if the relay offers it, then AUTH if credentials are configured —
// and hangs up with QUIT before MAIL FROM. It sends no message: a diagnostic
// that mails somebody on every page load is a diagnostic that gets the account
// rate-limited, and the operator already has POST /admin/mail/test for the
// question "does a whole message get through".
//
// It exists because reading the 220 greeting proved almost nothing. A05
// measured a relay whose certificate this instance refuses reporting `smtp: ok`
// on /admin/system while EVERY send failed, and the same for a relay that
// offers no AUTH against configured credentials. "Mail is fine" on that page did
// not cover the two steps that actually break.
//
// TWIN of the handshake in internal/mail/smtp.go: the ORDER and the conditions
// must stay identical (the same encryption mode, strict verification against
// Host, fail-closed when credentials meet a relay with no AUTH), or the probe
// answers a different question from the one the mailer asks. Anything changed
// there is changed here — which is why the mailer's SMTP transport calls THIS
// function for its own Probe rather than keeping a second copy of the dialogue.
type SMTPHandshake struct {
	Host     string
	Port     int
	Username string
	Password string
	// Encryption selects how the session is protected. The zero value ("") means
	// SMTPEncryptionAuto, which is what every caller predating admin-configurable
	// mail passed implicitly.
	Encryption SMTPEncryption
	// TLSConfig overrides the TLS client config (STARTTLS or implicit). Nil means
	// the production setting: verify the relay's certificate against Host at TLS
	// 1.2 minimum, exactly as the mailer does. Tests use it to trust a
	// self-signed cert. There is deliberately no skip-verify knob.
	TLSConfig *tls.Config
}

// SMTPEncryption is how one SMTP session is protected.
//
// Auto is the historical environment-configured behaviour and stays reachable
// for it alone: opportunistic STARTTLS silently downgrades to cleartext against
// a relay that stops advertising STARTTLS (a downgrade attack looks exactly like
// a relay reconfiguration), so an operator choosing a mode in the admin panel
// picks an explicit one and gets a failure instead of a silent plaintext send.
type SMTPEncryption string

const (
	// SMTPEncryptionAuto upgrades with STARTTLS when the relay offers it and
	// continues in cleartext when it does not.
	SMTPEncryptionAuto SMTPEncryption = "auto"
	// SMTPEncryptionSTARTTLS REQUIRES STARTTLS: a relay that does not advertise
	// it fails at the tls_unsupported stage rather than sending in the clear.
	SMTPEncryptionSTARTTLS SMTPEncryption = "starttls"
	// SMTPEncryptionTLS wraps the connection in TLS from the first byte
	// (implicit TLS, conventionally port 465). No STARTTLS is attempted.
	SMTPEncryptionTLS SMTPEncryption = "tls"
	// SMTPEncryptionNone never encrypts. Legitimate only for a relay on
	// localhost or a trusted private network.
	SMTPEncryptionNone SMTPEncryption = "none"
)

// SMTP handshake stages, in the order they are attempted. The stage is what
// makes the failure ACTIONABLE: "connection refused" and "certificate signed by
// unknown authority" send an operator to two completely different places.
const (
	SMTPStageDial     = "dial"
	SMTPStageGreeting = "greeting"
	SMTPStageSTARTTLS = "starttls"
	// SMTPStageTLSUnsupported is STARTTLS demanded (SMTPEncryptionSTARTTLS) by a
	// relay that does not advertise it. It is its own stage because the fix is
	// different from a failed upgrade: the operator picked the wrong mode or the
	// wrong port, and no certificate change will help.
	SMTPStageTLSUnsupported  = "tls_unsupported"
	SMTPStageAuthUnsupported = "auth_unsupported"
	SMTPStageAuth            = "auth"
	SMTPStageQuit            = "quit"
)

// SMTPHandshakeError is a failure at one named stage of the handshake.
type SMTPHandshakeError struct {
	Stage string
	Err   error
}

func (e *SMTPHandshakeError) Error() string {
	if e.Err == nil {
		return "smtp " + e.Stage + " failed"
	}
	return "smtp " + e.Stage + ": " + e.Err.Error()
}

func (e *SMTPHandshakeError) Unwrap() error { return e.Err }

// SMTPHandshakeResult records what the relay actually offered, so a caller can
// say more than pass/fail: an operator who believes their relay is encrypted
// wants to know when it advertised no STARTTLS at all.
type SMTPHandshakeResult struct {
	// STARTTLS is whether the relay advertised STARTTLS and the session was
	// upgraded.
	STARTTLS bool
	// Authenticated is whether AUTH was attempted AND accepted. False when no
	// credentials are configured — an anonymous relay is a supported shape.
	Authenticated bool
}

// SMTPTLSConfig is the client TLS configuration one handshake uses: the
// caller's override when it set one (tests trusting a self-signed cert), else
// the production setting — verify the relay's certificate against Host at TLS
// 1.2 minimum. There is no skip-verify path here by design.
func SMTPTLSConfig(h SMTPHandshake) *tls.Config {
	if h.TLSConfig != nil {
		return h.TLSConfig
	}
	return &tls.Config{ServerName: h.Host, MinVersion: tls.VersionTLS12}
}

// DialSMTP opens the TCP connection one SMTP conversation runs over, wrapping it
// in TLS from the first byte when the mode is implicit TLS (conventionally port
// 465, where there is no plaintext phase to upgrade out of). The returned error
// is an *SMTPHandshakeError at the dial stage.
//
// It is exported for internal/mail's SMTP transport, which must dial exactly the
// way the probe dials — a probe that reaches a relay the mailer cannot reach is
// worse than no probe.
func DialSMTP(ctx context.Context, h SMTPHandshake) (net.Conn, error) {
	addr := net.JoinHostPort(h.Host, strconv.Itoa(h.Port))
	if h.Encryption == SMTPEncryptionTLS {
		d := tls.Dialer{NetDialer: &net.Dialer{}, Config: SMTPTLSConfig(h)}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, &SMTPHandshakeError{Stage: SMTPStageDial, Err: err}
		}
		return conn, nil
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, &SMTPHandshakeError{Stage: SMTPStageDial, Err: err}
	}
	return conn, nil
}

// NegotiateSMTPTLS applies the encryption mode to a client that has just read
// the greeting, and reports whether the session is encrypted afterwards. The
// returned error is an *SMTPHandshakeError at the starttls or tls_unsupported
// stage.
//
// Exported for the same reason DialSMTP is: the mailer and the probe must make
// the same demand of the relay, and the only way to guarantee that is to have
// one implementation.
func NegotiateSMTPTLS(c *smtp.Client, h SMTPHandshake) (encrypted bool, err error) {
	switch h.Encryption {
	case SMTPEncryptionTLS:
		// Already encrypted by DialSMTP; STARTTLS inside TLS is meaningless.
		return true, nil
	case SMTPEncryptionNone:
		return false, nil
	case SMTPEncryptionSTARTTLS:
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return false, &SMTPHandshakeError{
				Stage: SMTPStageTLSUnsupported,
				Err:   errors.New("STARTTLS is required but the relay does not offer it"),
			}
		}
		if err := c.StartTLS(SMTPTLSConfig(h)); err != nil {
			return false, &SMTPHandshakeError{Stage: SMTPStageSTARTTLS, Err: err}
		}
		return true, nil
	default: // SMTPEncryptionAuto, and the zero value every pre-existing caller passes.
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return false, nil
		}
		if err := c.StartTLS(SMTPTLSConfig(h)); err != nil {
			return false, &SMTPHandshakeError{Stage: SMTPStageSTARTTLS, Err: err}
		}
		return true, nil
	}
}

// CheckSMTPHandshake performs the handshake. The returned error is always an
// *SMTPHandshakeError naming the stage. ctx bounds the whole conversation, not
// just the dial.
func CheckSMTPHandshake(ctx context.Context, h SMTPHandshake) (SMTPHandshakeResult, error) {
	var res SMTPHandshakeResult
	conn, err := DialSMTP(ctx, h)
	if err != nil {
		return res, err
	}
	// The deadline covers every read and write below, so a relay that accepts
	// the connection and then stops talking cannot hold the page open.
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	// NewClient reads the 220 greeting; a non-220 line (a proxy, a captive
	// portal, an HTTP server on the wrong port) fails here.
	c, err := smtp.NewClient(conn, h.Host)
	if err != nil {
		_ = conn.Close()
		return res, &SMTPHandshakeError{Stage: SMTPStageGreeting, Err: err}
	}
	defer func() { _ = c.Close() }()

	// Extension() sends EHLO on first use (falling back to HELO), so the
	// capability question and the greeting exchange are the same round trip.
	upgraded, err := NegotiateSMTPTLS(c, h)
	if err != nil {
		return res, err
	}
	res.STARTTLS = upgraded
	if h.Username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			// The mailer fails closed here rather than sending
			// unauthenticated, so the probe must call it a failure too.
			return res, &SMTPHandshakeError{
				Stage: SMTPStageAuthUnsupported,
				Err:   errors.New("smtp credentials configured but relay offers no AUTH"),
			}
		}
		if err := c.Auth(smtp.PlainAuth("", h.Username, h.Password, h.Host)); err != nil {
			return res, &SMTPHandshakeError{Stage: SMTPStageAuth, Err: err}
		}
		res.Authenticated = true
	}
	// QUIT rather than a bare close: an abandoned connection is a line in the
	// operator's relay log every time an admin opens the page.
	if err := c.Quit(); err != nil {
		return res, &SMTPHandshakeError{Stage: SMTPStageQuit, Err: fmt.Errorf("%w", err)}
	}
	return res, nil
}
