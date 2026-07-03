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
// Hard deletion (anonymising the account and purging its content) is the
// separate, irreversible internal/account.Service.Delete flow
// (product-decisions.md §1).
func (s *Service) DeactivateAccount(ctx context.Context, userID uuid.UUID, password string) error {
	if err := s.ConfirmPassword(ctx, userID, password); err != nil {
		return err
	}
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return err
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

// ConfirmPassword re-verifies the authenticated account's current password
// before a sensitive action (deactivation, the §1 hard delete). A passwordless
// account (OAuth-only: empty hash — bcrypt can never verify it) always fails
// with ErrInvalidPassword; an unknown/inactive subject is ErrAccountNotFound.
func (s *Service) ConfirmPassword(ctx context.Context, userID uuid.UUID, password string) error {
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := CheckPassword(user.PasswordHash, password); err != nil {
		return ErrInvalidPassword
	}
	return nil
}
