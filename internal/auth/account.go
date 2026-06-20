package auth

import (
	"context"

	"github.com/google/uuid"
)

// DeactivateAccount disables the authenticated account after confirming its
// password. A disabled account cannot log in (Login → ErrAccountDisabled) and
// its access tokens stop resolving (UserByID treats inactive as not-found); all
// of its sessions are revoked, so it is signed out everywhere. Deactivation is
// reversible by an administrator.
//
// Hard deletion (removing or anonymising the account and its content) is a
// separate flow gated on a data-retention/anonymisation policy and is not done
// here.
func (s *Service) DeactivateAccount(ctx context.Context, userID uuid.UUID, password string) error {
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := CheckPassword(user.PasswordHash, password); err != nil {
		return ErrInvalidPassword
	}
	if err := s.repo.DeactivateUser(ctx, user.ID); err != nil {
		return err
	}
	// Best-effort: the account is already disabled; failing to revoke sessions
	// must not fail the deactivation (a disabled account's tokens stop resolving
	// anyway).
	_ = s.repo.RevokeAllUserSessions(ctx, user.ID)
	return nil
}
