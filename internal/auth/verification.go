package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ErrInvalidVerificationToken means the email-verification token is unknown,
// already used, or expired. It is deliberately indistinct so a caller cannot
// probe which.
var ErrInvalidVerificationToken = errors.New("auth: invalid or expired verification token")

// verificationTokenBytes is the entropy of a raw email-verification token.
const verificationTokenBytes = 32

// generateVerificationToken returns a high-entropy opaque verification token and
// its storage hash. The raw token is delivered to the user exactly once (via the
// mailer); only the hash is persisted. SHA-256 is correct for a random token.
func generateVerificationToken() (raw, hash string, err error) {
	b := make([]byte, verificationTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashVerificationToken(raw), nil
}

// hashVerificationToken returns the hex SHA-256 of a raw verification token, used
// as the lookup key in email_verification_tokens.
func hashVerificationToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// RequestEmailVerification issues a single-use, expiring verification token for
// the account and hands it to the mailer. It is a no-op (returns nil) when the
// account's email is already verified. Any prior unused tokens are invalidated
// first, so only the newest link works.
func (s *Service) RequestEmailVerification(ctx context.Context, userID uuid.UUID) error {
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if user.EmailVerified {
		return nil
	}
	raw, hash, err := generateVerificationToken()
	if err != nil {
		return err
	}
	_ = s.repo.DeleteUnusedEmailVerificationTokens(ctx, user.ID)
	if _, err := s.repo.CreateEmailVerificationToken(ctx, sqlcgen.CreateEmailVerificationTokenParams{
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: s.now().Add(s.verifyTTL),
	}); err != nil {
		return err
	}
	return s.mailer.SendEmailVerification(ctx, user.Email, raw)
}

// VerifyEmail consumes a valid verification token: it marks the account's email
// verified and the token used. An unknown, used, or expired token yields
// ErrInvalidVerificationToken and changes nothing.
func (s *Service) VerifyEmail(ctx context.Context, rawToken string) error {
	row, err := s.repo.GetEmailVerificationToken(ctx, hashVerificationToken(rawToken))
	if err != nil {
		return ErrInvalidVerificationToken
	}
	if row.UsedAt.Valid || !row.ExpiresAt.After(s.now()) {
		return ErrInvalidVerificationToken
	}
	if err := s.repo.SetUserEmailVerified(ctx, row.UserID); err != nil {
		return err
	}
	// Best-effort: the email is already verified; failing to mark the token used
	// must not fail the request.
	_ = s.repo.MarkEmailVerificationTokenUsed(ctx, row.ID)
	return nil
}
