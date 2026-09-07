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
	// TokenKindEmailChange is the token that confirms a requested NEW address
	// (AUTH-05). It is captured under the NEW address, which is the only one it
	// is ever sent to.
	TokenKindEmailChange TokenKind = "email_change"
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
	// registrationDecisions are the approval/rejection notices the signup queue
	// sent an applicant.
	registrationDecisions []CapturedRegistrationDecision
	ownershipNotices      []CapturedOwnershipNotice
	// passwordChanged records the addresses that received a "your password was
	// changed" notice. No token is involved, so the address is the whole record.
	passwordChanged []string
	// emailChanged records the (old, new) pairs that received an "your address
	// was changed" notice at the OLD address. No token is involved.
	emailChanged []CapturedEmailChange
	testMessages []CapturedTestMessage
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

// SendPasswordChanged records that a password-changed notice was "sent". It
// carries no token, so there is nothing to store beyond the recipient — which is
// exactly what the acceptance harness asserts.
func (c *CaptureMailer) SendPasswordChanged(_ context.Context, email string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.passwordChanged = append(c.passwordChanged, email)
	return nil
}

// PasswordChangedNotices returns the addresses that received a password-changed
// notice, oldest first.
func (c *CaptureMailer) PasswordChangedNotices() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.passwordChanged))
	copy(out, c.passwordChanged)
	return out
}

// SendPasswordReset records the reset token instead of mailing it.
func (c *CaptureMailer) SendPasswordReset(_ context.Context, email, token string) error {
	c.store(TokenKindPasswordReset, email, token)
	return nil
}

// CapturedEmailChange is one "your email address was changed" notice recorded
// by the capture mailer instead of being delivered to the OLD address.
type CapturedEmailChange struct {
	OldEmail string
	NewEmail string
}

// SendEmailChangeVerification records the email-change confirmation token
// instead of mailing it, keyed on the NEW address it was addressed to.
func (c *CaptureMailer) SendEmailChangeVerification(_ context.Context, newEmail, token string) error {
	c.store(TokenKindEmailChange, newEmail, token)
	return nil
}

// SendEmailChanged records that the OLD address was told the account moved.
func (c *CaptureMailer) SendEmailChanged(_ context.Context, oldEmail, newEmail string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.emailChanged = append(c.emailChanged, CapturedEmailChange{OldEmail: oldEmail, NewEmail: newEmail})
	return nil
}

// EmailChangedNotices returns the notices sent to old addresses, oldest first.
func (c *CaptureMailer) EmailChangedNotices() []CapturedEmailChange {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]CapturedEmailChange(nil), c.emailChanged...)
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

// CapturedTestMessage is one admin mail-test probe recorded by the capture
// mailer instead of being delivered.
type CapturedTestMessage struct {
	To string
}

// SendTest records the admin mail-test probe instead of delivering it, so the
// dev/e2e stack — which has no relay — can still exercise the button end to end
// and prove the 202 path is real rather than mocked. Like every other capture,
// it lives in memory only and is never logged or persisted.
//
// It is deliberately NOT on auth.Mailer (that interface has four
// implementations and a pile of test fakes); httpapi asserts a narrow optional
// interface for it, so only this type and *mail.SMTP need to know about it.
func (c *CaptureMailer) SendTest(_ context.Context, to string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.testMessages = append(c.testMessages, CapturedTestMessage{To: to})
	return nil
}

// TestMessages returns a copy of every captured mail-test probe, in send order.
func (c *CaptureMailer) TestMessages() []CapturedTestMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]CapturedTestMessage(nil), c.testMessages...)
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

// CapturedRegistrationDecision is one approval or rejection notice the queue
// sent an applicant. Decision is "approved" or "rejected".
type CapturedRegistrationDecision struct {
	Decision       string
	Email          string
	Username       string
	SignInURL      string
	VerifyRequired bool
	Note           string
}

// SendRegistrationApproved records the approval notice instead of mailing it.
func (c *CaptureMailer) SendRegistrationApproved(_ context.Context, email, username, signInURL string, verifyRequired bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registrationDecisions = append(c.registrationDecisions, CapturedRegistrationDecision{
		Decision: "approved", Email: email, Username: username,
		SignInURL: signInURL, VerifyRequired: verifyRequired,
	})
	return nil
}

// SendRegistrationRejected records the rejection notice instead of mailing it.
func (c *CaptureMailer) SendRegistrationRejected(_ context.Context, email, username, note string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registrationDecisions = append(c.registrationDecisions, CapturedRegistrationDecision{
		Decision: "rejected", Email: email, Username: username, Note: note,
	})
	return nil
}

// CapturedOwnershipNotice is one side of an ownership-transfer notice. Party is
// "new_owner" or "former_owner".
type CapturedOwnershipNotice struct {
	Party       string
	Email       string
	Username    string
	Counterpart string
	ConsoleURL  string
}

// SendOwnershipTransferred records an ownership notice instead of mailing it.
func (c *CaptureMailer) SendOwnershipTransferred(_ context.Context, email, recipientUsername, counterpartUsername, consoleURL string, isNewOwner bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	party := "former_owner"
	if isNewOwner {
		party = "new_owner"
	}
	c.ownershipNotices = append(c.ownershipNotices, CapturedOwnershipNotice{
		Party: party, Email: email, Username: recipientUsername,
		Counterpart: counterpartUsername, ConsoleURL: consoleURL,
	})
	return nil
}

// OwnershipNotices returns a copy of every captured ownership-transfer notice,
// in send order.
func (c *CaptureMailer) OwnershipNotices() []CapturedOwnershipNotice {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]CapturedOwnershipNotice(nil), c.ownershipNotices...)
}

// RegistrationDecisions returns a copy of every captured signup decision
// notice, in send order.
func (c *CaptureMailer) RegistrationDecisions() []CapturedRegistrationDecision {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]CapturedRegistrationDecision(nil), c.registrationDecisions...)
}

// Latest returns the most recently captured raw token for the (kind, email) and
// whether one was found.
func (c *CaptureMailer) Latest(kind TokenKind, email string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.latest[captureKey(kind, email)]
	return t, ok
}
