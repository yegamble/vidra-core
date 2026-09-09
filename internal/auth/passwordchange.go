package auth

import (
	"context"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ChangePassword rotates the authenticated account's password after
// re-verifying the CURRENT one. It is the self-service counterpart to the
// mailbox-possession reset flow (reset.go): the reset proves you own the
// address, this proves you know the password, and neither can be driven by a
// stolen access token alone.
//
// On success every OTHER session is revoked and the caller's own survives, so
// the change signs out the other devices without signing out the browser it was
// made in. Because access tokens are session-bound (Claims.SessionID) and the
// auth middleware resolves the session per request, that revocation reaches the
// other devices' ACCESS tokens within one request, not within JWT_ACCESS_TTL.
//
// The caller's own session keeps its refresh token. Rotating it would gain
// nothing: it is a 256-bit random secret with no relation to the password, and
// the property that matters — killing whatever the attacker holds — is the
// revocation of every other session.
//
// A password-less account (OAuth/ATProto-only: an empty stored hash bcrypt can
// never verify) gets ErrPasswordNotSet, not "incorrect password" — it has no
// current password to supply, and the reset flow is the path that CAN set one.
func (s *Service) ChangePassword(ctx context.Context, userID uuid.UUID, currentPassword, newPassword, currentSessionID string) error {
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if user.PasswordHash == "" {
		return ErrPasswordNotSet
	}
	if err := CheckPassword(user.PasswordHash, currentPassword); err != nil {
		return ErrInvalidPassword
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := s.repo.UpdateUserPassword(ctx, sqlcgen.UpdateUserPasswordParams{
		ID:           user.ID,
		PasswordHash: hash,
	}); err != nil {
		return err
	}

	s.afterPasswordWritten(ctx, user, currentSessionID)
	return nil
}

// afterPasswordWritten is the shared tail of every self-service path that
// writes a new password hash: revoke the other sessions and mail the notice.
// Everything in it is best-effort — the credential has already changed, and
// failing to revoke or to mail must not report a failure the user would
// (wrongly) read as "my password is unchanged".
func (s *Service) afterPasswordWritten(ctx context.Context, user sqlcgen.User, currentSessionID string) {
	if sessionID, perr := uuid.Parse(currentSessionID); perr == nil {
		_ = s.repo.RevokeOtherUserSessions(ctx, sqlcgen.RevokeOtherUserSessionsParams{
			UserID: user.ID,
			ID:     sessionID,
		})
	} else {
		// No identifiable current session (a caller outside the normal
		// session-bound path): fall back to revoking everything rather than
		// leaving other devices signed in.
		_ = s.repo.RevokeAllUserSessions(ctx, user.ID)
	}
	// The security notice: the one signal that reaches a user whose password was
	// changed by somebody else. Never fails the change.
	//
	// For a provider-created account this notice goes to the synthetic
	// …@atproto.invalid address and is therefore undeliverable by construction.
	// It is still ATTEMPTED rather than skipped, because the address may already
	// have been moved to a real one (SC2's whole point) and this code cannot
	// know which; the mailer's failure is swallowed either way. What must not
	// happen is the UI claiming a notice was sent — see the set-password
	// handler, which says so honestly.
	_ = s.mailer.SendPasswordChanged(ctx, user.Email)
}

// SetPassword gives a PASSWORD-LESS account its first password, authorised by a
// step-up assertion instead of a current password.
//
// It is the missing half of ChangePassword, and A30 is why it exists: an
// account created by ATProto (or OIDC) login has exactly one credential, and
// every route to a second one asked for something it does not have. The reset
// flow needs a mailbox its placeholder address can never have; the change flow
// needs a password that is the thing being asked for. The proof this accepts
// instead is a completed OAuth round trip through the provider that already IS
// the account's credential — no weaker than the sign-in it just performed.
//
// The refusals are deliberate:
//
//	an account that HAS a password        → ErrPasswordAlreadySet (use the
//	                                        change route, which re-verifies it;
//	                                        otherwise a stolen access token
//	                                        would be a takeover primitive)
//	no / spent / expired / foreign token  → ErrStepUpRequired
//
// On success the consequences are the password change's, exactly: every OTHER
// session is revoked (access tokens included — they are session-bound) and the
// notice is attempted.
func (s *Service) SetPassword(ctx context.Context, userID uuid.UUID, newPassword, stepUpToken, currentSessionID string) error {
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	// Checked BEFORE the assertion is spent: an account with a password is on
	// the wrong route, and burning a single-use token to tell it so would make
	// the user redo the whole provider round trip for nothing.
	if user.PasswordHash != "" {
		return ErrPasswordAlreadySet
	}
	if _, err := s.ConsumeStepUp(ctx, user.ID, currentSessionID, stepUpToken); err != nil {
		return err
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := s.repo.UpdateUserPassword(ctx, sqlcgen.UpdateUserPasswordParams{
		ID:           user.ID,
		PasswordHash: hash,
	}); err != nil {
		return err
	}
	s.afterPasswordWritten(ctx, user, currentSessionID)
	return nil
}
