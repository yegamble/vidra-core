package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"net/smtp"
	"net/textproto"
	"os"
	"strings"
	"time"

	"github.com/vidra/vidra-core/internal/preflight"
)

// defaultSendTimeout bounds one SMTP conversation when the caller's context
// carries no earlier deadline, so a wedged relay cannot hang a request.
const defaultSendTimeout = 30 * time.Second

// Encryption is how an SMTP session is protected. The values are the ones the
// admin panel stores; EncryptionAuto is reachable only from the environment
// path (see preflight.SMTPEncryption for why an admin gets no silent downgrade).
type Encryption string

const (
	// EncryptionAuto upgrades with STARTTLS when the relay offers it and
	// continues in cleartext when it does not. The historical env behaviour.
	EncryptionAuto Encryption = "auto"
	// EncryptionSTARTTLS REQUIRES STARTTLS: a relay that does not advertise it
	// fails tls_failed rather than sending in the clear.
	EncryptionSTARTTLS Encryption = "starttls"
	// EncryptionTLS wraps the connection in TLS from the first byte (implicit
	// TLS, conventionally port 465).
	EncryptionTLS Encryption = "tls"
	// EncryptionNone never encrypts — legitimate only for a relay on localhost
	// or a trusted private network.
	EncryptionNone Encryption = "none"
)

// smtpTransportConfig is the SMTP transport's whole input.
type smtpTransportConfig struct {
	Host       string
	Port       int
	Username   string
	Password   string
	Encryption Encryption
	// TLSConfig overrides the client TLS settings (tests trusting a self-signed
	// cert). Nil means strict verification against Host; there is no
	// skip-verify knob and there will not be one.
	TLSConfig *tls.Config
}

// SMTPTransport delivers over an SMTP relay with the standard library's
// net/smtp. The dial, the encryption negotiation and the TLS settings are
// preflight's, not a second copy: the no-send probe on the admin status page
// and a real send must make identical demands of the relay, or the page
// answers a question nobody asked.
type SMTPTransport struct {
	cfg smtpTransportConfig
}

func newSMTPTransport(cfg smtpTransportConfig) *SMTPTransport {
	if cfg.Encryption == "" {
		cfg.Encryption = EncryptionAuto
	}
	return &SMTPTransport{cfg: cfg}
}

// Kind implements Transport.
func (t *SMTPTransport) Kind() string { return KindSMTP }

// Port is the relay port this transport dials. The admin UI needs it to
// recognise the host-blocks-submission-ports failure.
func (t *SMTPTransport) Port() int { return t.cfg.Port }

func (t *SMTPTransport) handshake() preflight.SMTPHandshake {
	return preflight.SMTPHandshake{
		Host:       t.cfg.Host,
		Port:       t.cfg.Port,
		Username:   t.cfg.Username,
		Password:   t.cfg.Password,
		Encryption: preflight.SMTPEncryption(t.cfg.Encryption),
		TLSConfig:  t.cfg.TLSConfig,
	}
}

// Send runs one SMTP conversation: dial (implicit TLS when configured), EHLO,
// STARTTLS per the encryption mode, AUTH PLAIN when credentials are configured,
// then a single-recipient plain-text message.
func (t *SMTPTransport) Send(ctx context.Context, m Message) error {
	if err := validateMessage(m); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultSendTimeout)
		defer cancel()
	}

	h := t.handshake()
	conn, err := preflight.DialSMTP(ctx, h)
	if err != nil {
		return t.classify(err)
	}
	// The context deadline bounds the whole conversation, not just the dial.
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	c, err := smtp.NewClient(conn, t.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return t.fail(ReasonConnectFailed, wrap("smtp greeting", err))
	}
	defer func() { _ = c.Close() }()

	if _, err := preflight.NegotiateSMTPTLS(c, h); err != nil {
		return t.classify(err)
	}
	if t.cfg.Username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			// Credentials are configured but the relay offers no AUTH — fail
			// closed rather than silently sending unauthenticated.
			return t.fail(ReasonAuthFailed, errors.New("smtp credentials configured but relay offers no AUTH"))
		}
		auth := smtp.PlainAuth("", t.cfg.Username, t.cfg.Password, t.cfg.Host)
		if err := c.Auth(auth); err != nil {
			return t.fail(ReasonAuthFailed, wrap("smtp auth", err))
		}
	}
	if err := c.Mail(m.From.Address); err != nil {
		// A relay that refuses MAIL FROM is refusing the SENDER — the usual
		// cause is a From the relay is not allowed to send for.
		return t.fail(t.dialogueReason(err, ReasonSenderRejected), wrap("MAIL FROM", err))
	}
	if err := c.Rcpt(m.To); err != nil {
		return t.fail(t.dialogueReason(err, ReasonRejected), wrap("RCPT TO", err))
	}
	w, err := c.Data()
	if err != nil {
		return t.fail(t.dialogueReason(err, ReasonRejected), wrap("DATA", err))
	}
	if _, err := w.Write(render(m)); err != nil {
		_ = w.Close()
		return t.fail(ReasonRejected, wrap("write message", err))
	}
	if err := w.Close(); err != nil {
		return t.fail(t.dialogueReason(err, ReasonRejected), wrap("finish message", err))
	}
	if err := c.Quit(); err != nil {
		return t.fail(t.dialogueReason(err, ReasonRejected), wrap("quit", err))
	}
	return nil
}

// Probe is preflight's no-send handshake: EHLO, the configured encryption, AUTH
// when credentials exist, QUIT before MAIL FROM. It sends nothing, so it can run
// on every admin page load without costing the instance a message.
func (t *SMTPTransport) Probe(ctx context.Context) ProbeResult {
	if _, err := preflight.CheckSMTPHandshake(ctx, t.handshake()); err != nil {
		return ProbeResult{Err: t.classify(err)}
	}
	// AUTH either succeeded or there were no credentials to prove. Unlike an
	// HTTP provider there is nothing left that only a real send would show.
	return ProbeResult{Verified: true}
}

// classify turns a preflight handshake failure into a *SendError. The stage is
// what makes it actionable, so it maps stage-by-stage rather than sniffing text.
func (t *SMTPTransport) classify(err error) error {
	var he *preflight.SMTPHandshakeError
	if !errors.As(err, &he) {
		return t.fail(ReasonConnectFailed, err)
	}
	switch he.Stage {
	case preflight.SMTPStageDial:
		// A dial that ran out of time and one that was refused send an operator
		// to different places, and on 25/465/587 both are the signature of a
		// host that blocks outbound submission (DigitalOcean blocks all three).
		if isTimeout(he.Err) {
			return t.fail(ReasonTimeout, he)
		}
		return t.fail(ReasonConnectFailed, he)
	case preflight.SMTPStageGreeting:
		return t.fail(ReasonConnectFailed, he)
	case preflight.SMTPStageSTARTTLS, preflight.SMTPStageTLSUnsupported:
		return t.fail(ReasonTLSFailed, he)
	case preflight.SMTPStageAuthUnsupported, preflight.SMTPStageAuth:
		return t.fail(ReasonAuthFailed, he)
	default:
		return t.fail(ReasonRejected, he)
	}
}

// dialogueReason reads the relay's reply code: 4xx is "try later" (the relay
// says so itself), 5xx is the caller's problem and keeps the stage's own
// meaning.
func (t *SMTPTransport) dialogueReason(err error, on5xx Reason) Reason {
	var pe *textproto.Error
	if !errors.As(err, &pe) {
		if isTimeout(err) {
			return ReasonTimeout
		}
		return on5xx
	}
	switch {
	case pe.Code == 421 || pe.Code == 451 || pe.Code == 452:
		// 421 service not available / 451 local error / 452 insufficient
		// storage: the relay is asking for later, not telling us we are wrong.
		return ReasonProviderUnavailable
	case pe.Code == 450:
		// Mailbox unavailable, and what relays overwhelmingly use for "you are
		// sending too fast" (4.7.1 policy throttles).
		return ReasonRateLimited
	case pe.Code == 530 || pe.Code == 534 || pe.Code == 535:
		return ReasonAuthFailed
	case pe.Code >= 400 && pe.Code < 500:
		return ReasonProviderUnavailable
	}
	return on5xx
}

func (t *SMTPTransport) fail(r Reason, err error) error {
	return &SendError{Reason: r, Err: err, Port: t.cfg.Port}
}

// render produces a minimal RFC 5322 plain-text message with CRLF line endings.
// The recipient, sender and optional reply-to are validated by the caller; every
// other header value is sanitized — nothing can inject headers.
func render(m Message) []byte {
	var b strings.Builder
	b.WriteString("From: " + formatAddress(m.From) + "\r\n")
	b.WriteString("To: " + m.To + "\r\n")
	if m.ReplyTo != "" {
		b.WriteString("Reply-To: " + sanitizeHeader(m.ReplyTo) + "\r\n")
	}
	b.WriteString("Subject: " + sanitizeHeader(m.Subject) + "\r\n")
	b.WriteString("Date: " + time.Now().UTC().Format("Mon, 02 Jan 2006 15:04:05 -0700") + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(m.Text, "\n", "\r\n"))
	return []byte(b.String())
}

// wrap keeps the stage name in the message the way the pre-transport mailer
// did, so relay logs and operator reports still line up.
func wrap(stage string, err error) error {
	return errors.New(stage + ": " + err.Error())
}

// isTimeout reports whether err is a deadline expiry rather than a refusal.
func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var te interface{ Timeout() bool }
	return errors.As(err, &te) && te.Timeout()
}
