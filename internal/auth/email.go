package auth

import "context"

// PasswordResetMailer delivers a password-reset token to a user out-of-band
// (typically email). It is an adapter boundary: the platform generates and
// stores the token regardless, and a concrete provider (SMTP, transactional
// email API) is injected via WithMailer when one is configured.
//
// Implementations MUST NOT log, trace, or otherwise persist the raw token — it
// is a single-use credential (see .ralph/specs/observability.md).
type PasswordResetMailer interface {
	SendPasswordReset(ctx context.Context, email, token string) error
}

// noopMailer is the default mailer. With no email provider configured it drops
// the message: reset tokens are still generated, stored, and consumable (so the
// flow is testable and an operator could surface them), but nothing leaves the
// process. It logs nothing by design — the token is a credential.
type noopMailer struct{}

func (noopMailer) SendPasswordReset(context.Context, string, string) error { return nil }
