// Package mail delivers account-security email (password reset, email
// verification) over SMTP. It implements the auth.Mailer adapter boundary with
// the standard library only: net/smtp with opportunistic STARTTLS (used
// whenever the relay offers it) and optional AUTH PLAIN credentials.
//
// Security rules (see .ralph/specs/observability.md):
//   - The raw token is a single-use credential. This package never logs — the
//     token exists only in the message body handed to the relay.
//   - The SMTP password is a secret; it is never logged either (the
//     "smtp_password" key is on the observability sensitive-key denylist).
//   - net/smtp's PlainAuth refuses to send credentials over an unencrypted
//     connection unless the host is localhost, so AUTH never leaks the
//     password to a plaintext network path.
package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// defaultSendTimeout bounds one SMTP conversation when the caller's context
// carries no earlier deadline, so a wedged relay cannot hang a request.
const defaultSendTimeout = 30 * time.Second

// Config holds SMTP delivery settings. Host+From are required (validated by
// config.Load when MAIL_ENABLED); Username/Password are optional AUTH PLAIN
// credentials; InstanceName labels the subject lines.
type Config struct {
	Host         string
	Port         int
	Username     string
	Password     string
	From         string
	InstanceName string
}

// SMTP is an auth.Mailer that delivers over an SMTP relay.
type SMTP struct {
	cfg       Config
	tlsConfig *tls.Config

	// Email customization seam (config-parity W6): provider funcs over the
	// instance-settings overlay, read per send so an admin change applies
	// without a restart. Applied at the single send() seam every message
	// funnels through. Nil funcs (or empty values) are no-ops — and when
	// MAIL_ENABLED is off no SMTP mailer exists at all, so the settings are
	// trivially inert.
	subjectPrefix func() string // email_subject_prefix; {instance_name} substituted
	bodySignature func() string // email_body_signature; appended after a blank line
	instanceName  func() string // EFFECTIVE instance name for the substitution (else cfg.InstanceName)
}

// Option customises the SMTP mailer.
type Option func(*SMTP)

// WithTLSConfig overrides the TLS client configuration used for STARTTLS
// (default: server-name verification against Config.Host). Used by tests to
// trust a self-signed test certificate; production wiring should not need it.
func WithTLSConfig(c *tls.Config) Option {
	return func(s *SMTP) {
		if c != nil {
			s.tlsConfig = c
		}
	}
}

// WithSubjectPrefixFunc wires the email_subject_prefix provider (config-parity
// W6): a non-empty value is prepended to every outgoing subject, with
// {instance_name} substituted from the effective instance name.
func WithSubjectPrefixFunc(f func() string) Option {
	return func(s *SMTP) { s.subjectPrefix = f }
}

// WithBodySignatureFunc wires the email_body_signature provider (config-parity
// W6): a non-empty value is appended to every outgoing plaintext body after a
// blank line.
func WithBodySignatureFunc(f func() string) Option {
	return func(s *SMTP) { s.bodySignature = f }
}

// WithInstanceNameFunc wires the EFFECTIVE instance-name provider (the
// DB-overlaid instance_name) used for the {instance_name} substitution. When
// unset, the boot-time Config.InstanceName is used.
func WithInstanceNameFunc(f func() string) Option {
	return func(s *SMTP) { s.instanceName = f }
}

// NewSMTP builds the SMTP mailer.
func NewSMTP(cfg Config, opts ...Option) *SMTP {
	s := &SMTP{
		cfg:       cfg,
		tlsConfig: &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// SendContactForm delivers a visitor contact-form message to the operator's
// contact address. The visitor's address rides as Reply-To (never the envelope
// sender, which stays the configured From), so the operator can answer
// directly without the relay rejecting a spoofed sender. All header values are
// sanitized; nothing here is ever logged (visitor + operator addresses and the
// body are PII).
func (s *SMTP) SendContactForm(ctx context.Context, to, fromName, fromEmail, subject, body string) error {
	if strings.ContainsAny(fromEmail, "\r\n") || strings.TrimSpace(fromEmail) == "" {
		return fmt.Errorf("mail: invalid reply-to address")
	}
	subj := "[" + s.cfg.InstanceName + " contact] " + subject
	msg := "New contact-form message on " + s.cfg.InstanceName + ".\n\n" +
		"From: " + sanitizeHeader(fromName) + " <" + fromEmail + ">\n" +
		"Subject: " + sanitizeHeader(subject) + "\n\n" +
		body + "\n"
	return s.send(ctx, to, fromEmail, subj, msg)
}

// SendTest delivers the admin "does outbound mail actually work" probe to the
// instance's own contact address. It is deliberately NOT part of auth.Mailer:
// the interface has four implementations and a pile of test fakes, and widening
// it for one admin button would churn all of them. httpapi asserts a narrow
// optional interface for this method instead.
//
// The message goes through the same send() seam every other message does, so it
// picks up the configured subject prefix and body signature — a test that
// bypassed the decoration would prove the relay works and prove nothing about
// what recipients actually see. The instance is named from the EFFECTIVE
// (DB-overlaid) name rather than the boot config: an admin who just renamed the
// instance and immediately sends a test should not read the old name back.
func (s *SMTP) SendTest(ctx context.Context, to string) error {
	name := s.effectiveInstanceName()
	subject := "Test message from " + name
	body := "This is a test message, sent from the admin settings of " + name + ".\n\n" +
		"If you are reading it, this instance's outbound mail works: password resets, " +
		"email verification and operator alerts can reach the people they are addressed to.\n\n" +
		"Nobody needs to do anything about this message.\n"
	return s.send(ctx, to, "", subject, body)
}

// SendNewReportAlert tells the operator a user filed an abuse report — the
// push half of the moderation queue (the in-app staff notification is the
// other half). targetType names what was reported; the reporter's identity is
// deliberately NOT included (it lives in the queue, and this message may
// transit third-party relays). The reason is the reporter's free text and
// rides in the body only, never in a header.
func (s *SMTP) SendNewReportAlert(ctx context.Context, to, targetType, reason, queueURL string) error {
	subj := "[" + s.cfg.InstanceName + "] New " + sanitizeHeader(targetType) + " report"
	msg := "A new " + targetType + " abuse report was filed on " + s.cfg.InstanceName + ".\n\n" +
		"Reason: " + reason + "\n"
	if queueURL != "" {
		msg += "\nReview it in the moderation queue: " + queueURL + "\n"
	}
	return s.send(ctx, to, "", subj, msg)
}

// SendRegistrationApproved tells an applicant on an approval-gated instance
// that their signup was accepted. It carries NO credential: the password is the
// one they chose when they applied, and this message must never be able to
// change it. verifyRequired says the instance also holds new accounts for email
// verification, in which case the message says so rather than promising a
// sign-in that would be refused.
func (s *SMTP) SendRegistrationApproved(ctx context.Context, email, username, signInURL string, verifyRequired bool) error {
	name := s.effectiveInstanceName()
	subject := "Your account on " + name + " was approved"
	body := "Hi,\n\n" +
		"Your request for an account on " + name + " was approved, and the account " +
		"\"" + username + "\" is now active.\n\n" +
		"Sign in with the username and password you chose when you applied — " +
		"this message carries no password and no sign-in link that could change one.\n"
	if signInURL != "" {
		body += "\nSign in here: " + signInURL + "\n"
	}
	if verifyRequired {
		body += "\nOne more step: this instance also asks new accounts to confirm their " +
			"email address, so look for a separate message with a confirmation code. " +
			"Sign-in stays closed until that code is entered.\n"
	}
	return s.send(ctx, email, "", subject, body)
}

// SendRegistrationRejected tells an applicant their signup was declined, and
// passes on the reviewer's note when one was written — it is prose meant for
// exactly this reader, which is why it travels here and never into the audit
// ledger. The note rides in the body only, never in a header.
func (s *SMTP) SendRegistrationRejected(ctx context.Context, email, username, note string) error {
	name := s.effectiveInstanceName()
	subject := "Your account request on " + name + " was not approved"
	body := "Hi,\n\n" +
		"Your request for an account on " + name + " (username \"" + username + "\") " +
		"was reviewed and not approved, so no account was created.\n"
	if strings.TrimSpace(note) != "" {
		body += "\nThe reviewer left this note:\n\n" + note + "\n"
	}
	body += "\nIf you think this was a mistake, contact the people who run " + name + ".\n"
	return s.send(ctx, email, "", subject, body)
}

// SendOwnershipTransferred tells one party to an instance-ownership transfer
// that it happened. Two sends, one per side, because the two sides need
// different sentences: the new owner is told what they now hold and where the
// console is, the former owner is told what they gave up and — the part that
// matters if this was not their idea — that it took their password to do it and
// who to contact. Neither body carries a credential or a link that could change
// one, so the message is safe to leave in a mailbox.
func (s *SMTP) SendOwnershipTransferred(ctx context.Context, email, recipientUsername, counterpartUsername, consoleURL string, isNewOwner bool) error {
	name := s.effectiveInstanceName()
	var subject, body string
	if isNewOwner {
		subject = "You are now the owner of " + name
		body = "Hi,\n\n" +
			"\"" + counterpartUsername + "\" transferred ownership of " + name + " to your " +
			"account, \"" + recipientUsername + "\".\n\n" +
			"You are now the one administrator this instance protects: other admins " +
			"cannot change your role, deactivate you or delete your account, and you " +
			"are the only account that can hand ownership on again.\n"
		if consoleURL != "" {
			body += "\nThe admin console is here: " + consoleURL + "\n"
		}
		body += "\nThis message carries no password and no sign-in link. " +
			"Use the credentials you already have.\n"
	} else {
		subject = "You are no longer the owner of " + name
		body = "Hi,\n\n" +
			"Ownership of " + name + " moved from your account, \"" + recipientUsername +
			"\", to \"" + counterpartUsername + "\".\n\n" +
			"You are still an administrator and nothing else about your account changed. " +
			"What you gave up is the owner protection — another administrator can now " +
			"change your role, deactivate you or delete your account — and the ability " +
			"to transfer ownership.\n\n" +
			"This required your password, so if it was not you, change your password now " +
			"and contact the people who run " + name + ".\n"
	}
	return s.send(ctx, email, "", subject, body)
}

// SendPasswordReset delivers a password-reset token. The token appears only in
// the message body; it is never logged.
func (s *SMTP) SendPasswordReset(ctx context.Context, email, token string) error {
	subject := "Reset your password on " + s.cfg.InstanceName
	body := "Hi,\n\n" +
		"Someone (hopefully you) asked to reset the password for the " + s.cfg.InstanceName +
		" account tied to this address.\n\n" +
		"Your password reset code is:\n\n" +
		"    " + token + "\n\n" +
		"Enter it on the \"Reset password\" page to choose a new password. " +
		"The code can be used once and expires soon.\n\n" +
		"If you did not ask for this, you can ignore this message — your password is unchanged.\n"
	return s.send(ctx, email, "", subject, body)
}

// SendPasswordChanged tells an account its password was just changed. It is the
// after-the-fact security notice — the one signal that reaches a user whose
// password was changed by somebody else — so it deliberately carries no token,
// no link that could change anything, and nothing about the new password.
func (s *SMTP) SendPasswordChanged(ctx context.Context, email string) error {
	subject := "Your password on " + s.cfg.InstanceName + " was changed"
	body := "Hi,\n\n" +
		"The password for your " + s.cfg.InstanceName + " account was just changed, " +
		"and every other signed-in device was signed out.\n\n" +
		"If that was you, there is nothing to do.\n\n" +
		"If it was NOT you, someone else may have access to this account: use " +
		"\"Forgot password\" to take it back, and check your other accounts that " +
		"share the same password.\n"
	return s.send(ctx, email, "", subject, body)
}

// SendEmailVerification delivers an email-verification token. The token appears
// only in the message body; it is never logged.
func (s *SMTP) SendEmailVerification(ctx context.Context, email, token string) error {
	subject := "Confirm your email address on " + s.cfg.InstanceName
	body := "Hi,\n\n" +
		"To confirm this address for your " + s.cfg.InstanceName + " account, use the code below.\n\n" +
		"Your email verification code is:\n\n" +
		"    " + token + "\n\n" +
		"Enter it on the \"Verify email\" page. The code can be used once and expires soon.\n\n" +
		"If you did not create an account, you can ignore this message.\n"
	return s.send(ctx, email, "", subject, body)
}

// SendEmailChangeVerification delivers the token that confirms a requested NEW
// address. It goes to the new address only: it IS the possession proof, so
// sending it anywhere else would prove nothing. The token appears in the body
// and is never logged.
func (s *SMTP) SendEmailChangeVerification(ctx context.Context, newEmail, token string) error {
	name := s.effectiveInstanceName()
	subject := "Confirm your new email address on " + name
	body := "Hi,\n\n" +
		"Someone (hopefully you) asked to change the email address on a " + name +
		" account to this one.\n\n" +
		"Your confirmation code is:\n\n" +
		"    " + token + "\n\n" +
		"Enter it on the \"Confirm email change\" page while signed in to that account. " +
		"The code can be used once and expires soon.\n\n" +
		"Until it is used, the account keeps its current address. " +
		"If you did not ask for this, you can ignore this message.\n"
	return s.send(ctx, newEmail, "", subject, body)
}

// SendEmailChanged tells the OLD address that the account has moved to a new
// one. It is the after-the-fact security notice and the LAST message that
// reaches the mailbox the user still controls, so it names the new address —
// without it the reader cannot tell what happened or prove it to an operator.
// It carries no token and no link that could change anything.
func (s *SMTP) SendEmailChanged(ctx context.Context, oldEmail, newEmail string) error {
	name := s.effectiveInstanceName()
	subject := "The email address on your " + name + " account was changed"
	body := "Hi,\n\n" +
		"The email address for your " + name + " account was just changed to " +
		newEmail + ", after that address was confirmed.\n\n" +
		"If that was you, there is nothing to do — this message is the last one " +
		"this address will receive.\n\n" +
		"If it was NOT you, someone else may have access to this account. " +
		"Contact the instance operator: the sign-in address has moved, so " +
		"\"Forgot password\" on this address will no longer reach it.\n"
	return s.send(ctx, oldEmail, "", subject, body)
}

// send runs one SMTP conversation: EHLO, STARTTLS when offered, AUTH PLAIN when
// credentials are configured, then a single-recipient plain-text message.
// replyTo, when non-empty, is stamped as the Reply-To header (validated by the
// caller).
func (s *SMTP) send(ctx context.Context, to, replyTo, subject, body string) error {
	if strings.ContainsAny(to, "\r\n") || strings.TrimSpace(to) == "" {
		return fmt.Errorf("mail: invalid recipient address")
	}
	// Email customization (config-parity W6), applied at this single seam so
	// every sender path gets it. Header injection is impossible downstream:
	// message() sanitizes the subject and the signature is body text.
	subject = s.decorateSubject(subject)
	body = s.decorateBody(body)
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultSendTimeout)
		defer cancel()
	}

	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("mail: dial smtp relay: %w", err)
	}
	// The context deadline bounds the whole conversation, not just the dial.
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("mail: smtp greeting: %w", err)
	}
	defer func() { _ = c.Close() }()

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(s.tlsConfig); err != nil {
			return fmt.Errorf("mail: starttls: %w", err)
		}
	}
	if s.cfg.Username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			// Credentials are configured but the relay offers no AUTH — fail
			// closed rather than silently sending unauthenticated.
			return fmt.Errorf("mail: smtp credentials configured but relay offers no AUTH")
		}
		auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("mail: smtp auth: %w", err)
		}
	}
	if err := c.Mail(s.cfg.From); err != nil {
		return fmt.Errorf("mail: MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("mail: RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail: DATA: %w", err)
	}
	if _, err := w.Write(message(s.cfg.From, to, replyTo, subject, body)); err != nil {
		_ = w.Close()
		return fmt.Errorf("mail: write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: finish message: %w", err)
	}
	return c.Quit()
}

// message renders a minimal RFC 5322 plain-text message with CRLF line endings.
// The recipient and optional reply-to are validated by the caller; every other
// header value is sanitized — nothing can inject headers.
func message(from, to, replyTo, subject, body string) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	if replyTo != "" {
		b.WriteString("Reply-To: " + sanitizeHeader(replyTo) + "\r\n")
	}
	b.WriteString("Subject: " + sanitizeHeader(subject) + "\r\n")
	b.WriteString("Date: " + time.Now().UTC().Format("Mon, 02 Jan 2006 15:04:05 -0700") + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	return []byte(b.String())
}

// sanitizeHeader strips CR/LF so a header value can never break the envelope.
func sanitizeHeader(v string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(v)
}

// effectiveInstanceName is the name substituted for {instance_name}: the
// runtime provider (DB-overlaid instance_name) when wired, else the boot-time
// config value.
func (s *SMTP) effectiveInstanceName() string {
	if s.instanceName != nil {
		if name := strings.TrimSpace(s.instanceName()); name != "" {
			return name
		}
	}
	return s.cfg.InstanceName
}

// decorateSubject prepends the email_subject_prefix (config-parity W6) with
// {instance_name} substituted. Empty/whitespace prefix (or no provider) is a
// no-op.
func (s *SMTP) decorateSubject(subject string) string {
	if s.subjectPrefix == nil {
		return subject
	}
	prefix := strings.TrimSpace(s.subjectPrefix())
	if prefix == "" {
		return subject
	}
	prefix = strings.ReplaceAll(prefix, "{instance_name}", s.effectiveInstanceName())
	return prefix + " " + subject
}

// decorateBody appends the email_body_signature (config-parity W6) after a
// blank line. Empty/whitespace signature (or no provider) is a no-op.
func (s *SMTP) decorateBody(body string) string {
	if s.bodySignature == nil {
		return body
	}
	sig := strings.TrimSpace(s.bodySignature())
	if sig == "" {
		return body
	}
	return strings.TrimRight(body, "\n") + "\n\n" + sig + "\n"
}
