// Package admin implements administrator user management for vidra-core: listing
// and searching accounts, and editing a user's role and active flag. It is
// HTTP-agnostic and testable without a server. These operations are restricted
// to admins by the HTTP layer (requireRole); the service enforces the safety
// invariants (an admin cannot demote or deactivate themselves into lockout).
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
	ListUsers(ctx context.Context, arg sqlcgen.ListUsersParams) ([]sqlcgen.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlcgen.User, error)
	AdminUpdateUser(ctx context.Context, arg sqlcgen.AdminUpdateUserParams) (sqlcgen.User, error)
	RevokeAllUserSessions(ctx context.Context, userID uuid.UUID) error
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
// username/email substring (empty query returns all). The caller clamps
// limit/offset.
func (s *Service) ListUsers(ctx context.Context, query string, limit, offset int32) ([]sqlcgen.User, error) {
	return s.repo.ListUsers(ctx, sqlcgen.ListUsersParams{
		Query:        query,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
}

// UpdateUser edits a user's role and/or active flag. nil fields are unchanged.
// An admin may not demote (to a non-admin role) or deactivate their own account
// — that returns ErrSelfChange to avoid locking the last admin out. An unknown
// target returns ErrNotFound. Deactivating a user revokes their sessions so the
// ban takes effect immediately (best-effort).
func (s *Service) UpdateUser(ctx context.Context, callerID, targetID uuid.UUID, role *string, isActive *bool) (sqlcgen.User, error) {
	if _, err := s.repo.GetUserByID(ctx, targetID); err != nil {
		return sqlcgen.User{}, ErrNotFound
	}
	if callerID == targetID {
		demoting := role != nil && *role != RoleAdmin
		deactivating := isActive != nil && !*isActive
		if demoting || deactivating {
			return sqlcgen.User{}, ErrSelfChange
		}
	}
	updated, err := s.repo.AdminUpdateUser(ctx, sqlcgen.AdminUpdateUserParams{
		ID:       targetID,
		Role:     role,
		IsActive: isActive,
	})
	if err != nil {
		return sqlcgen.User{}, err
	}
	if isActive != nil && !*isActive {
		// Best-effort: a disabled account's tokens stop resolving anyway.
		_ = s.repo.RevokeAllUserSessions(ctx, targetID)
	}
	return updated, nil
}
