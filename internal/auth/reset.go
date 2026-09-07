package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ErrInvalidResetToken means the password-reset token is unknown, already used,
// or expired. It is deliberately indistinct so a caller cannot probe which.
var ErrInvalidResetToken = errors.New("auth: invalid or expired reset token")

// ErrMailDelivery marks a failure to HAND a message to the relay, as opposed to
// a failure to do the work the message reports. It exists so the password-reset
// endpoint can keep its single answer: RequestPasswordReset already returns nil
// for an address that matches nothing, and before this sentinel it returned the
// relay's error verbatim for an address that DOES match — which made a broken
// relay an account-existence oracle (500 = registered, 202 = not; proven live
// 2026-09-07). The HTTP layer answers 202 and audits the failure; the operator
// learns the relay is down from the smtp component on /admin/system, which is
// where that fact belongs.
var ErrMailDelivery = errors.New("auth: could not deliver the message")

// resetTokenBytes is the entropy of a raw password-reset token (256 bits).
const resetTokenBytes = 32

// generateResetToken returns a high-entropy opaque reset token and its storage
// hash. The raw token is delivered to the user exactly once (via the mailer);
// only the hash is persisted. Like the refresh token, it is already random, so a
// fast hash (SHA-256) is correct here — bcrypt is only for low-entropy passwords.
func generateResetToken() (raw, hash string, err error) {
	b := make([]byte, resetTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashResetToken(raw), nil
}

// hashResetToken returns the hex SHA-256 of a raw reset token, used as the lookup
// key in password_reset_tokens.
func hashResetToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// RequestPasswordReset issues a single-use, expiring reset token for the account
// with the given email and hands it to the mailer. It is enumeration-safe: it
// returns nil whether or not the email matches an active account, so a caller
// cannot learn which emails are registered. Any prior unused tokens for the
// account are invalidated first, so only the newest link works.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	user, err := s.repo.GetUserByEmail(ctx, strings.TrimSpace(email))
	if err != nil || !user.IsActive {
		return nil
	}
	raw, hash, err := generateResetToken()
	if err != nil {
		return err
	}
	_ = s.repo.DeleteUnusedPasswordResetTokens(ctx, user.ID)
	if _, err := s.repo.CreatePasswordResetToken(ctx, sqlcgen.CreatePasswordResetTokenParams{
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: s.now().Add(s.resetTTL),
	}); err != nil {
		return err
	}
	if err := s.mailer.SendPasswordReset(ctx, user.Email, raw); err != nil {
		return fmt.Errorf("%w: %w", ErrMailDelivery, err)
	}
	return nil
}

// ResetPassword consumes a valid reset token: it sets a new password, marks the
// token used, and revokes every session for the account (forcing re-login
// everywhere). An unknown, used, or expired token yields ErrInvalidResetToken
// and changes nothing.
func (s *Service) ResetPassword(ctx context.Context, rawToken, newPassword string) error {
	row, err := s.repo.GetPasswordResetToken(ctx, hashResetToken(rawToken))
	if err != nil {
		return ErrInvalidResetToken
	}
	if row.UsedAt.Valid || !row.ExpiresAt.After(s.now()) {
		return ErrInvalidResetToken
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := s.repo.UpdateUserPassword(ctx, sqlcgen.UpdateUserPasswordParams{
		ID:           row.UserID,
		PasswordHash: hash,
	}); err != nil {
		return err
	}
	// Best-effort: the password is already changed; failing to mark the token
	// used or to revoke sessions must not fail the reset.
	_ = s.repo.MarkPasswordResetTokenUsed(ctx, row.ID)
	_ = s.repo.RevokeAllUserSessions(ctx, row.UserID)
	return nil
}
