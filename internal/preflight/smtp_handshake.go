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
// TWIN of the handshake in internal/mail/smtp.go send(): the ORDER and the
// conditions must stay identical (opportunistic STARTTLS, strict verification
// against Host, fail-closed when credentials meet a relay with no AUTH), or the
// probe answers a different question from the one the mailer asks. Anything
// changed there is changed here.
type SMTPHandshake struct {
	Host     string
	Port     int
	Username string
	Password string
	// TLSConfig overrides the STARTTLS client config. Nil means the production
	// setting: verify the relay's certificate against Host at TLS 1.2 minimum,
	// exactly as the mailer does. Tests use it to trust a self-signed cert.
	TLSConfig *tls.Config
}

// SMTP handshake stages, in the order they are attempted. The stage is what
// makes the failure ACTIONABLE: "connection refused" and "certificate signed by
// unknown authority" send an operator to two completely different places.
const (
	SMTPStageDial            = "dial"
	SMTPStageGreeting        = "greeting"
	SMTPStageSTARTTLS        = "starttls"
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

// CheckSMTPHandshake performs the handshake. The returned error is always an
// *SMTPHandshakeError naming the stage. ctx bounds the whole conversation, not
// just the dial.
func CheckSMTPHandshake(ctx context.Context, h SMTPHandshake) (SMTPHandshakeResult, error) {
	var res SMTPHandshakeResult
	addr := net.JoinHostPort(h.Host, strconv.Itoa(h.Port))
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return res, &SMTPHandshakeError{Stage: SMTPStageDial, Err: err}
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
	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsCfg := h.TLSConfig
		if tlsCfg == nil {
			tlsCfg = &tls.Config{ServerName: h.Host, MinVersion: tls.VersionTLS12}
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return res, &SMTPHandshakeError{Stage: SMTPStageSTARTTLS, Err: err}
		}
		res.STARTTLS = true
	}
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
