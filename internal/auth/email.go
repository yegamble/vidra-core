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
	// SendPasswordChanged tells an account its password was just changed. It
	// carries NO credential — it is the after-the-fact security notice that
	// reaches a user whose password was changed by somebody else, so it must be
	// sent on success and must never fail the change.
	SendPasswordChanged(ctx context.Context, email string) error
	SendEmailVerification(ctx context.Context, email, token string) error
	// SendContactForm delivers a visitor contact-form message to the operator
	// address `to`, identifying the visitor by fromName/fromEmail (surfaced as
	// Reply-To by real transports, never as the envelope sender).
	SendContactForm(ctx context.Context, to, fromName, fromEmail, subject, body string) error
	// SendNewReportAlert tells the operator address `to` that a user filed an
	// abuse report: targetType is what was reported (video, comment, account,
	// remote_video, message), reason is the reporter's free text, queueURL
	// links to the moderation queue ("" when no public base URL is set). The
	// reporter's identity is deliberately NOT included — it lives in the
	// queue, not in email.
	SendNewReportAlert(ctx context.Context, to, targetType, reason, queueURL string) error
}

// noopMailer is the default mailer. With no email provider configured it drops
// the message: tokens are still generated, stored, and consumable (so the flow
// is testable and an operator could surface them), but nothing leaves the
// process. It logs nothing by design — the token is a credential.
type noopMailer struct{}

func (noopMailer) SendPasswordReset(context.Context, string, string) error     { return nil }
func (noopMailer) SendPasswordChanged(context.Context, string) error           { return nil }
func (noopMailer) SendEmailVerification(context.Context, string, string) error { return nil }
func (noopMailer) SendContactForm(context.Context, string, string, string, string, string) error {
	return nil
}
func (noopMailer) SendNewReportAlert(context.Context, string, string, string, string) error {
	return nil
}
