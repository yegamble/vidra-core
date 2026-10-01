package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Host-side owner recovery (`owner-recovery` api subcommand): the way back for
// an owner who lost their password AND has no working mail. Its authority is
// shell access to the host, not anything an HTTP caller can present.

var (
	// ErrNoInstanceOwner: nobody holds the owner marker, so the instance is
	// unclaimed. The message names the operator's next step.
	ErrNoInstanceOwner = errors.New("auth: this instance has no owner yet — claim it with `vidra claim`")
	// ErrOwnerAccountDisabled: a reset would succeed and the login after it
	// would still be refused, so say so instead of handing over a dead end.
	ErrOwnerAccountDisabled = errors.New("auth: the instance owner's account is deactivated; a password reset cannot restore access")
)

// OwnerRecovery carries the raw token (shown once, never stored). It
// deliberately omits the owner's address: the operator knows who the owner is,
// and an address must not travel through logs or a pipe.
type OwnerRecovery struct {
	Token      string
	ExpiresAt  time.Time
	MFARemoved bool
	OwnerID    uuid.UUID
}

// IssueOwnerRecovery mints an ordinary single-use reset token for the owner
// without sending mail; ResetPassword spends it and revokes every session. Prior
// unused tokens are invalidated, so only the newest link works.
//
// Without removeMFA the owner still needs their authenticator at the NEXT login
// (a reset only sets the password). removeMFA is checked for availability BEFORE
// minting, so a token is never paired with a removal that cannot happen; the
// factor is deleted AFTER the token is stored, so a failed mint cannot strand the
// owner with neither. Removal revokes all sessions at once; an owner with no MFA
// is not an error (MFARemoved=false).
func (s *Service) IssueOwnerRecovery(ctx context.Context, removeMFA bool) (OwnerRecovery, error) {
	if removeMFA && s.mfaRepo == nil {
		return OwnerRecovery{}, ErrMFAUnavailable
	}
	owner, err := s.repo.GetInstanceOwner(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return OwnerRecovery{}, ErrNoInstanceOwner
	}
	if err != nil {
		return OwnerRecovery{}, err
	}
	if !owner.IsActive {
		return OwnerRecovery{}, ErrOwnerAccountDisabled
	}

	raw, hash, err := generateResetToken()
	if err != nil {
		return OwnerRecovery{}, err
	}
	// Fatal here, unlike the web request: a surviving older token would defeat
	// "only the newest link works".
	if err := s.repo.DeleteUnusedPasswordResetTokens(ctx, owner.ID); err != nil {
		return OwnerRecovery{}, err
	}
	expires := s.now().Add(s.resetTTL)
	if _, err := s.repo.CreatePasswordResetToken(ctx, sqlcgen.CreatePasswordResetTokenParams{
		UserID: owner.ID, TokenHash: hash, ExpiresAt: expires,
	}); err != nil {
		return OwnerRecovery{}, err
	}
	res := OwnerRecovery{Token: raw, ExpiresAt: expires, OwnerID: owner.ID}

	if removeMFA {
		n, err := s.mfaRepo.DeleteUserMFA(ctx, owner.ID)
		if err != nil {
			return OwnerRecovery{}, err
		}
		if n > 0 {
			if err := s.mfaRepo.DeleteRecoveryCodes(ctx, owner.ID); err != nil {
				return OwnerRecovery{}, err
			}
			// Revokes every session; the notice is a no-op where no mailer is wired.
			s.afterTwoFactorRemoved(ctx, owner, "", true)
			res.MFARemoved = true
		}
	}
	return res, nil
}
