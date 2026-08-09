package auth

import (
	"context"
	"sync"
)

// TokenKind identifies which account-security flow a captured token belongs to.
type TokenKind string

const (
	TokenKindPasswordReset     TokenKind = "reset"
	TokenKindEmailVerification TokenKind = "verification"
)

// CaptureMailer is a DEVELOPMENT/TEST-ONLY Mailer that keeps the most recent raw
// token per (kind, email) in memory instead of delivering it. It exists so an
// automated end-to-end test (or a local developer) can complete the
// password-reset / email-verification round trip without a real email provider.
//
// It MUST NOT be used in production: it makes single-use credentials retrievable
// by anyone who can reach the dev endpoint that reads it. It is wired only when
// DEV_MAIL_CAPTURE_ENABLED is set, and the process logs a loud warning on boot.
// Tokens are held in memory only — never logged, never written to disk or the DB
// — so a process restart clears them.
type CaptureMailer struct {
	mu           sync.Mutex
	latest       map[string]string
	contacts     []CapturedContact
	reportAlerts []CapturedReportAlert
}

// CapturedContact is one contact-form message recorded by the capture mailer
// instead of being delivered.
type CapturedContact struct {
	To        string
	FromName  string
	FromEmail string
	Subject   string
	Body      string
}

// NewCaptureMailer returns an empty capture mailer.
func NewCaptureMailer() *CaptureMailer {
	return &CaptureMailer{latest: make(map[string]string)}
}

// captureKey namespaces by kind so a reset and a verification token for the same
// email never collide. The NUL separator cannot appear in either component.
func captureKey(kind TokenKind, email string) string {
	return string(kind) + "\x00" + email
}

func (c *CaptureMailer) store(kind TokenKind, email, token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.latest[captureKey(kind, email)] = token
}

// SendPasswordReset records the reset token instead of mailing it.
func (c *CaptureMailer) SendPasswordReset(_ context.Context, email, token string) error {
	c.store(TokenKindPasswordReset, email, token)
	return nil
}

// SendEmailVerification records the verification token instead of mailing it.
func (c *CaptureMailer) SendEmailVerification(_ context.Context, email, token string) error {
	c.store(TokenKindEmailVerification, email, token)
	return nil
}

// SendContactForm records the contact-form message instead of mailing it (in
// memory only — never logged or persisted, like the tokens).
func (c *CaptureMailer) SendContactForm(_ context.Context, to, fromName, fromEmail, subject, body string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.contacts = append(c.contacts, CapturedContact{
		To: to, FromName: fromName, FromEmail: fromEmail, Subject: subject, Body: body,
	})
	return nil
}

// Contacts returns a copy of every captured contact-form message, in send order.
func (c *CaptureMailer) Contacts() []CapturedContact {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]CapturedContact(nil), c.contacts...)
}

// CapturedReportAlert is one new-report operator alert recorded by the capture
// mailer instead of being delivered.
type CapturedReportAlert struct {
	To         string
	TargetType string
	Reason     string
	QueueURL   string
}

// SendNewReportAlert records the new-report operator alert instead of mailing
// it (in memory only — never logged or persisted).
func (c *CaptureMailer) SendNewReportAlert(_ context.Context, to, targetType, reason, queueURL string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reportAlerts = append(c.reportAlerts, CapturedReportAlert{
		To: to, TargetType: targetType, Reason: reason, QueueURL: queueURL,
	})
	return nil
}

// ReportAlerts returns a copy of every captured new-report alert, in send order.
func (c *CaptureMailer) ReportAlerts() []CapturedReportAlert {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]CapturedReportAlert(nil), c.reportAlerts...)
}

// Latest returns the most recently captured raw token for the (kind, email) and
// whether one was found.
func (c *CaptureMailer) Latest(kind TokenKind, email string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.latest[captureKey(kind, email)]
	return t, ok
}
