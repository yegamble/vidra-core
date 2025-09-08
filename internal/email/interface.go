package email

import "context"

// EmailService defines the interface for email operations
type EmailService interface {
	SendVerificationEmail(ctx context.Context, toEmail, username, token, code string) error
	SendResendVerificationEmail(ctx context.Context, toEmail, username, token, code string) error
	SendPasswordResetEmail(ctx context.Context, toEmail, username, token string) error
}