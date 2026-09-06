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

	// Everything below is best-effort: the credential has already changed, and
	// failing to revoke or to mail must not report a failure the user would
	// (wrongly) read as "my password is unchanged".
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
	_ = s.mailer.SendPasswordChanged(ctx, user.Email)
	return nil
}
