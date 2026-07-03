// Package admin implements administrator user management for vidra-core: listing
// and searching accounts, and editing a user's role, active flag, and storage
// quota. It is HTTP-agnostic and testable without a server. These operations are
// restricted to admins by the HTTP layer (requireRole); the service enforces the
// safety invariants (an admin cannot demote or deactivate themselves into lockout).
package admin

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// User roles.
const (
	RoleUser      = "user"
	RoleModerator = "moderator"
	RoleAdmin     = "admin"
)

// ValidRole reports whether r is an assignable role.
func ValidRole(r string) bool {
	return r == RoleUser || r == RoleModerator || r == RoleAdmin
}

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrNotFound means no user matches the lookup.
	ErrNotFound = errors.New("admin: user not found")
	// ErrSelfChange means an admin tried to demote or deactivate their own account.
	ErrSelfChange = errors.New("admin: cannot demote or deactivate yourself")
)

// Repository is the data access the admin service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	ListUsers(ctx context.Context, arg sqlcgen.ListUsersParams) ([]sqlcgen.ListUsersRow, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlcgen.User, error)
	AdminUpdateUser(ctx context.Context, arg sqlcgen.AdminUpdateUserParams) (sqlcgen.User, error)
	RevokeAllUserSessions(ctx context.Context, userID uuid.UUID) error
	SumUserStorageUsage(ctx context.Context, ownerID uuid.UUID) (int64, error)
}

// Service holds the admin application logic.
type Service struct {
	repo Repository
}

// NewService builds the admin service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// ListUsers returns accounts, newest first, optionally filtered by a
// username/email substring (empty query returns all), each row carrying the
// account's current storage usage. The caller clamps limit/offset.
func (s *Service) ListUsers(ctx context.Context, query string, limit, offset int32) ([]sqlcgen.ListUsersRow, error) {
	return s.repo.ListUsers(ctx, sqlcgen.ListUsersParams{
		Query:        query,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
}

// UpdateUserInput is a partial admin edit of an account; nil Role/IsActive/
// EmailVerified/BypassQuarantine are unchanged. The storage quota is tri-state:
// untouched unless SetStorageQuota is true, in which case a nil
// StorageQuotaBytes resets the account to the instance default (NULL) and a
// value overrides it (0 = unlimited). EmailVerified lets an admin mark an
// address confirmed without the token round-trip (or revoke that
// confirmation). BypassQuarantine exempts a trusted account from the
// QUARANTINE_NEW_UPLOADS gate (product-decisions.md §10/§11).
type UpdateUserInput struct {
	Role              *string
	IsActive          *bool
	EmailVerified     *bool
	BypassQuarantine  *bool
	SetStorageQuota   bool
	StorageQuotaBytes *int64
}

// UpdateUser edits a user's role, active flag, and/or storage quota. An admin
// may not demote (to a non-admin role) or deactivate their own account — that
// returns ErrSelfChange to avoid locking the last admin out (quota changes on
// oneself are allowed; they carry no lockout risk). An unknown target returns
// ErrNotFound. Deactivating a user revokes their sessions so the ban takes
// effect immediately (best-effort).
func (s *Service) UpdateUser(ctx context.Context, callerID, targetID uuid.UUID, in UpdateUserInput) (sqlcgen.User, error) {
	if _, err := s.repo.GetUserByID(ctx, targetID); err != nil {
		return sqlcgen.User{}, ErrNotFound
	}
	if callerID == targetID {
		demoting := in.Role != nil && *in.Role != RoleAdmin
		deactivating := in.IsActive != nil && !*in.IsActive
		if demoting || deactivating {
			return sqlcgen.User{}, ErrSelfChange
		}
	}
	updated, err := s.repo.AdminUpdateUser(ctx, sqlcgen.AdminUpdateUserParams{
		ID:                targetID,
		Role:              in.Role,
		IsActive:          in.IsActive,
		EmailVerified:     in.EmailVerified,
		BypassQuarantine:  in.BypassQuarantine,
		SetStorageQuota:   in.SetStorageQuota,
		StorageQuotaBytes: in.StorageQuotaBytes,
	})
	if err != nil {
		return sqlcgen.User{}, err
	}
	if in.IsActive != nil && !*in.IsActive {
		// Best-effort: a disabled account's tokens stop resolving anyway.
		_ = s.repo.RevokeAllUserSessions(ctx, targetID)
	}
	return updated, nil
}

// StorageUsed returns an account's current storage usage in bytes (the same
// aggregate the quota service enforces against), for the admin user view.
func (s *Service) StorageUsed(ctx context.Context, userID uuid.UUID) (int64, error) {
	return s.repo.SumUserStorageUsage(ctx, userID)
}
