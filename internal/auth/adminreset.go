package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/pgconv"
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
	return s.mintResetLink(ctx, targetID)
}

// mintResetLink is the token half shared by AdminIssueResetLink and
// AdminIssueSetPasswordLink: it supersedes every unused token and stores a new one.
func (s *Service) mintResetLink(ctx context.Context, targetID uuid.UUID) (AdminResetLink, error) {
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

// AdminCreateAccountInput is an administrator-created account. There is no
// password: the account holder sets the first one through a set-password link.
type AdminCreateAccountInput struct {
	Username string
	Email    string
	Role     string
}

// AdminCreateAccount creates a password-less account for an administrator after
// they re-confirm their own password (checked FIRST, like AdminIssueResetLink).
// It goes through the same gates and the same CreateUser as signup, so the
// owner-claim gate, the username/email/channel-handle uniqueness (ErrConflict /
// ErrHandleReserved) and the new-user history default are identical. An empty
// hash can never verify, so password login stays impossible until the link from
// AdminIssueSetPasswordLink is redeemed. Signup creates no default channel
// either (channels are created by the user), so none is created here.
func (s *Service) AdminCreateAccount(ctx context.Context, adminID uuid.UUID, adminPassword string, in AdminCreateAccountInput) (sqlcgen.User, error) {
	if err := s.confirmAdminPassword(ctx, adminID, adminPassword); err != nil {
		return sqlcgen.User{}, err
	}
	if err := s.refuseIfOwnerUnclaimed(ctx); err != nil {
		return sqlcgen.User{}, err
	}
	user, err := s.repo.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username:       strings.TrimSpace(in.Username),
		Email:          strings.TrimSpace(in.Email),
		PasswordHash:   "",
		Role:           in.Role,
		HistoryEnabled: s.newUserHistoryEnabled(),
	})
	if err != nil {
		if pgconv.IsUniqueViolation(err) {
			return sqlcgen.User{}, nameConflict(err)
		}
		return sqlcgen.User{}, err
	}
	return user, nil
}

// AdminIssueSetPasswordLink mints the first-password link for an account the
// caller just created. Unlike AdminIssueResetLink it has no role gate: the
// caller is AdminCreateAccount's handler holding a brand-new account (possibly
// staff), never an arbitrary id.
func (s *Service) AdminIssueSetPasswordLink(ctx context.Context, targetID uuid.UUID) (AdminResetLink, error) {
	return s.mintResetLink(ctx, targetID)
}

// AdminSendSetPasswordLink emails the set-password token through the ordinary
// reset mail. A relay failure is ErrMailDelivery-wrapped.
func (s *Service) AdminSendSetPasswordLink(ctx context.Context, email, token string) error {
	if err := s.mailer.SendPasswordReset(ctx, email, token); err != nil {
		return fmt.Errorf("%w: %w", ErrMailDelivery, err)
	}
	return nil
}

// AdminWithdrawResetLink deletes the target's unused reset tokens. The handler
// calls it when the audit write fails: a link that was never shown must not stay
// redeemable for the rest of its TTL. Best-effort by nature (the caller is
// already failing); the error is returned so it can be logged.
func (s *Service) AdminWithdrawResetLink(ctx context.Context, targetID uuid.UUID) error {
	return s.repo.DeleteUnusedPasswordResetTokens(ctx, targetID)
}
