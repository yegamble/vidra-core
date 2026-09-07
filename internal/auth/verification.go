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
	"time"

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

// VerificationResendCooldown is the minimum spacing between anonymous
// verification resends for ONE address. It is a SEND throttle, not a request
// throttle: the endpoint must answer every caller the same 202, so a repeat
// inside the window is accepted and simply does not put a second message in the
// mailbox. The per-IP auth limiter is the other half — this one bounds what a
// distributed caller can do to a single inbox.
const VerificationResendCooldown = 60 * time.Second

// ResendEmailVerification re-issues a verification message for an address,
// WITHOUT a session. It exists because the account that needs it is precisely
// the one that cannot log in: with the verification gate on, registration
// returns 202 and no session, login answers 403 email_verification_required, and
// the only resend that shipped sat behind requireAuth. A registrant who lost the
// message was stuck until an admin flipped email_verified by hand.
//
// It is enumeration-safe by construction: it returns nil for an address that
// matches nothing, for one that is already verified, for a disabled account, and
// for a repeat inside the cooldown, so the caller cannot tell any of those apart
// from a real send. The one error it can return is a delivery failure, wrapped
// in ErrMailDelivery — which the HTTP layer answers 202 for, for the same reason
// the password-reset route does: a broken relay must not become the oracle the
// endpoint was written to avoid.
func (s *Service) ResendEmailVerification(ctx context.Context, email string) error {
	user, err := s.repo.GetUserByEmail(ctx, strings.TrimSpace(email))
	if err != nil || !user.IsActive || user.EmailVerified {
		return nil
	}
	if at, err := s.repo.LatestUnusedEmailVerificationTokenAt(ctx, user.ID); err == nil {
		if s.now().Sub(at) < VerificationResendCooldown {
			return nil
		}
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
	if err := s.mailer.SendEmailVerification(ctx, user.Email, raw); err != nil {
		return fmt.Errorf("%w: %w", ErrMailDelivery, err)
	}
	return nil
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
