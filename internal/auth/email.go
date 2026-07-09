package auth

import "context"

// Mailer delivers account-security tokens to a user out-of-band (typically
// email). It is an adapter boundary: the platform generates and stores tokens
// regardless, and a concrete provider (SMTP, transactional email API) is
// injected via WithMailer when one is configured.
//
// Implementations MUST NOT log, trace, or otherwise persist a raw token — each
// is a single-use credential (see .ralph/specs/observability.md). Contact-form
// sends carry visitor + operator addresses and a free-text body (PII):
// implementations must not log or persist those either.
type Mailer interface {
	SendPasswordReset(ctx context.Context, email, token string) error
	SendEmailVerification(ctx context.Context, email, token string) error
	// SendContactForm delivers a visitor contact-form message to the operator
	// address `to`, identifying the visitor by fromName/fromEmail (surfaced as
	// Reply-To by real transports, never as the envelope sender).
	SendContactForm(ctx context.Context, to, fromName, fromEmail, subject, body string) error
}

// noopMailer is the default mailer. With no email provider configured it drops
// the message: tokens are still generated, stored, and consumable (so the flow
// is testable and an operator could surface them), but nothing leaves the
// process. It logs nothing by design — the token is a credential.
type noopMailer struct{}

func (noopMailer) SendPasswordReset(context.Context, string, string) error     { return nil }
func (noopMailer) SendEmailVerification(context.Context, string, string) error { return nil }
func (noopMailer) SendContactForm(context.Context, string, string, string, string, string) error {
	return nil
}
