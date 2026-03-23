package auth

import "context"

type EmailVerificationServiceInterface interface {
	VerifyEmailWithToken(ctx context.Context, token string) error

	VerifyEmailWithCode(ctx context.Context, code, userID string) error

	ResendVerificationEmail(ctx context.Context, email string) error
}
