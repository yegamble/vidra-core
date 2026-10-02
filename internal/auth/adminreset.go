package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Administrator-minted reset link: the recovery path for an ordinary user who
// is locked out on an instance with no working mail. Same token store and
// redemption as the emailed link (ResetPassword spends it and revokes every
// session); only the delivery differs — the admin hands the link over.
var (
	// ErrResetTargetInactive: a link to a deactivated or tombstoned account
	// would be redeemable and the login after it still refused.
	ErrResetTargetInactive = errors.New("auth: that account is deactivated")
	// ErrResetTargetOwner and ErrResetTargetStaff: a link is a takeover
	// credential, so an admin must not be able to mint one for the account that
	// outranks or equals them. Staff recover through the owner or `vidra owner reset`.
	ErrResetTargetOwner = errors.New("auth: the instance owner recovers through the host, not an admin link")
	ErrResetTargetStaff = errors.New("auth: only ordinary accounts can be given a reset link by an admin")
)

// AdminResetLink carries the raw token (shown once, never stored).
type AdminResetLink struct {
	Token     string
	ExpiresAt time.Time
}

// confirmAdminPassword re-verifies the ACTING administrator's own password — the
// shared step-up of AdminRemoveTOTP and AdminIssueResetLink.
func (s *Service) confirmAdminPassword(ctx context.Context, adminID uuid.UUID, password string) error {
	admin, err := s.UserByID(ctx, adminID)
	if err != nil {
		return err
	}
	if admin.PasswordHash == "" {
		return ErrPasswordNotSet
	}
	if err := CheckPassword(admin.PasswordHash, password); err != nil {
		return ErrInvalidPassword
	}
	return nil
}

// AdminIssueResetLink mints a single-use reset token for an ordinary user after
// the admin re-confirms their own password. The password is checked BEFORE the
// target is looked up, so a caller without it learns nothing about which ids
// exist. Prior unused tokens for the target are invalidated (fatal on failure,
// as in IssueOwnerRecovery: a surviving older link would defeat "only the newest
// works"). The caller must audit BEFORE showing the token.
func (s *Service) AdminIssueResetLink(ctx context.Context, adminID uuid.UUID, adminPassword string, targetID uuid.UUID) (AdminResetLink, error) {
	if err := s.confirmAdminPassword(ctx, adminID, adminPassword); err != nil {
		return AdminResetLink{}, err
	}
	target, err := s.repo.GetUserByID(ctx, targetID)
	if err != nil {
		return AdminResetLink{}, ErrAccountNotFound
	}
	switch {
	case !target.IsActive || target.DeletedAt.Valid:
		return AdminResetLink{}, ErrResetTargetInactive
	case target.IsOwner:
		return AdminResetLink{}, ErrResetTargetOwner
	case target.Role != "user":
		return AdminResetLink{}, ErrResetTargetStaff
	}
	raw, hash, err := generateResetToken()
	if err != nil {
		return AdminResetLink{}, err
	}
	if err := s.repo.DeleteUnusedPasswordResetTokens(ctx, targetID); err != nil {
		return AdminResetLink{}, err
	}
	expires := s.now().Add(s.resetTTL)
	if _, err := s.repo.CreatePasswordResetToken(ctx, sqlcgen.CreatePasswordResetTokenParams{
		UserID: targetID, TokenHash: hash, ExpiresAt: expires,
	}); err != nil {
		return AdminResetLink{}, err
	}
	return AdminResetLink{Token: raw, ExpiresAt: expires}, nil
}

// AdminWithdrawResetLink deletes the target's unused reset tokens. The handler
// calls it when the audit write fails: a link that was never shown must not stay
// redeemable for the rest of its TTL. Best-effort by nature (the caller is
// already failing); the error is returned so it can be logged.
func (s *Service) AdminWithdrawResetLink(ctx context.Context, targetID uuid.UUID) error {
	return s.repo.DeleteUnusedPasswordResetTokens(ctx, targetID)
}
