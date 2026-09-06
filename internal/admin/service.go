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
	// ErrDeletedAccount means an admin tried to reactivate a hard-deleted
	// (tombstoned) account. Deletion anonymises the row irreversibly — the
	// username, address, display name and bio are gone — so re-enabling it
	// would republish a profile page for a person who asked to be erased
	// without restoring anything they had. The row stays listed and readable.
	ErrDeletedAccount = errors.New("admin: account is deleted and cannot be reactivated")
	// ErrOwnerProtected means an admin tried to demote, deactivate or delete
	// THE instance owner — the account that redeemed the first-run claim token
	// (0104/0131). Vidra has no owner ROLE: the owner holds `admin` like anyone
	// else, so without this guard the person who installed the instance could be
	// removed by an admin they themselves promoted. The owner's own self-guards
	// are unaffected — this only restrains other admins.
	ErrOwnerProtected = errors.New("admin: the instance owner cannot be demoted, deactivated or deleted by another admin")
	// ErrLastAdmin means a change would leave the instance with no account that
	// can reach its own admin console: it removes the last active admin's role,
	// access or account. Reachable in practice from the SELF-service side — the
	// sole admin deactivating or deleting their own account through
	// /auth/me/deactivate or DELETE /auth/me, where the admin routes' self-guard
	// does not apply. The recovery from zero admins is a database edit, so this
	// is refused rather than warned about.
	ErrLastAdmin = errors.New("admin: this is the last active admin; the instance would have none")
)

// Repository is the data access the admin service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	ListUsers(ctx context.Context, arg sqlcgen.ListUsersParams) ([]sqlcgen.ListUsersRow, error)
	CountUsersMatching(ctx context.Context, query string) (int64, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlcgen.User, error)
	AdminUpdateUser(ctx context.Context, arg sqlcgen.AdminUpdateUserParams) (sqlcgen.User, error)
	RevokeAllUserSessions(ctx context.Context, userID uuid.UUID) error
	SumUserStorageUsage(ctx context.Context, ownerID uuid.UUID) (int64, error)
	// CountActiveAdmins counts accounts that can still administer the instance
	// (role='admin' AND is_active AND deleted_at IS NULL) — the set the
	// last-admin guard must never empty.
	CountActiveAdmins(ctx context.Context) (int64, error)
	// Instance-wide aggregates for the admin overview (Stats). Each is a single
	// trivially-real COUNT/SUM over a committed column — no time-series store.
	CountUsers(ctx context.Context) (int64, error)
	CountPublicVideos(ctx context.Context) (int64, error)
	CountComments(ctx context.Context) (int64, error)
	SumAllStorageUsage(ctx context.Context) (int64, error)
	CountFederatedPeers(ctx context.Context) (int64, error)
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

// CountUsersMatching returns how many accounts ListUsers would return for the
// same query, ignoring pagination — the total a caller needs to know how many
// pages exist. It counts the SAME set the page came from, so it moves with the
// search filter rather than reporting the instance total next to a filtered page.
func (s *Service) CountUsersMatching(ctx context.Context, query string) (int64, error) {
	return s.repo.CountUsersMatching(ctx, query)
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

// UpdateResult carries both sides of an admin edit. The audit envelope's
// `changes` array wants before/after pairs, and the pre-image only exists inside
// the service — UpdateUser already reads the target to run its guards, so
// handing it back costs nothing, while recovering it in the handler would cost
// a second read on every admin edit.
type UpdateResult struct {
	Before sqlcgen.User
	After  sqlcgen.User
}

// UpdateUser edits a user's role, active flag, and/or storage quota. An admin
// may not demote (to a non-admin role) or deactivate their own account — that
// returns ErrSelfChange to avoid locking the last admin out (quota changes on
// oneself are allowed; they carry no lockout risk). Reactivating a tombstoned
// account returns ErrDeletedAccount. An unknown target returns ErrNotFound.
// Deactivating a user revokes their sessions so the ban takes effect
// immediately (best-effort).
func (s *Service) UpdateUser(ctx context.Context, callerID, targetID uuid.UUID, in UpdateUserInput) (sqlcgen.User, error) {
	res, err := s.UpdateUserDetailed(ctx, callerID, targetID, in)
	return res.After, err
}

// UpdateUserDetailed is UpdateUser plus the row as it was before the write.
func (s *Service) UpdateUserDetailed(ctx context.Context, callerID, targetID uuid.UUID, in UpdateUserInput) (UpdateResult, error) {
	target, err := s.repo.GetUserByID(ctx, targetID)
	if err != nil {
		return UpdateResult{}, ErrNotFound
	}
	// A tombstone accepts NO admin write. Reactivation is refused because
	// deletion is the one admin action with no inverse — it anonymises the row
	// in place, so "Reactivate" would restore a public profile for
	// `deleted-<suffix>` and nothing else. The rest (role, quota, both flags)
	// is refused because those writes are inert: the session lookup requires
	// deleted_at IS NULL, so the row can never authenticate and no rule they
	// set can ever apply. Accepting them made the console report a change that
	// would never take effect, which is worse than refusing the write.
	if target.DeletedAt.Valid {
		return UpdateResult{}, ErrDeletedAccount
	}
	// What this edit takes away, in the two senses that can lock an instance
	// out: the admin role, and the ability to sign in at all.
	demoting := in.Role != nil && *in.Role != RoleAdmin
	deactivating := in.IsActive != nil && !*in.IsActive
	if callerID == targetID {
		if demoting || deactivating {
			return UpdateResult{}, ErrSelfChange
		}
	} else if target.IsOwner && (demoting || deactivating) {
		// Checked AFTER the self guard so the owner keeps reading the message it
		// always read for its own edits.
		return UpdateResult{}, ErrOwnerProtected
	}
	if demoting || deactivating {
		if err := s.ensureAdminRemains(ctx, target); err != nil {
			return UpdateResult{}, err
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
		return UpdateResult{}, err
	}
	if in.IsActive != nil && !*in.IsActive {
		// Best-effort: a disabled account's tokens stop resolving anyway.
		_ = s.repo.RevokeAllUserSessions(ctx, targetID)
	}
	return UpdateResult{Before: target, After: updated}, nil
}

// ensureAdminRemains refuses a change that would strip the last active admin of
// its role or its access. target must be the CURRENT row: an account that is not
// already a live admin cannot be the last one, so the count is only paid when
// the change actually matters.
//
// This is a check-then-write guard, not an atomic one. It closes every
// sequential path; two admins removing each other in the same instant can still
// both pass their own count (each sees the other still live) — closing that
// needs SERIALIZABLE or an advisory lock around the write and is recorded as a
// follow-up rather than half-built here.
func (s *Service) ensureAdminRemains(ctx context.Context, target sqlcgen.User) error {
	if target.Role != RoleAdmin || !target.IsActive || target.DeletedAt.Valid {
		return nil
	}
	n, err := s.repo.CountActiveAdmins(ctx)
	if err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastAdmin
	}
	return nil
}

// CheckAdminRemoval reports whether targetID may lose its administrator standing
// — by demotion, deactivation or deletion. It is the guard's entry point for the
// SELF-service routes (POST /auth/me/deactivate, DELETE /auth/me), which have no
// admin caller to compare against and are the only paths from which an instance
// can actually reach zero admins. An unknown id is not an admin, so it passes.
func (s *Service) CheckAdminRemoval(ctx context.Context, targetID uuid.UUID) error {
	target, err := s.repo.GetUserByID(ctx, targetID)
	if err != nil {
		return nil
	}
	return s.ensureAdminRemains(ctx, target)
}

// CheckDelete is the pre-flight for an admin hard-deleting another account
// (DELETE /admin/users/{id}). It refuses removing the instance owner and
// removing the last active admin, with the same errors the PATCH route uses, so
// the two routes cannot drift apart. Deleting an already-deleted row is left
// alone: the delete service answers that with its own 404, and re-refusing it
// here would change a shipped answer.
func (s *Service) CheckDelete(ctx context.Context, callerID, targetID uuid.UUID) error {
	target, err := s.repo.GetUserByID(ctx, targetID)
	if err != nil {
		return ErrNotFound
	}
	if target.DeletedAt.Valid {
		return nil
	}
	if target.IsOwner && callerID != targetID {
		return ErrOwnerProtected
	}
	return s.ensureAdminRemains(ctx, target)
}

// StorageUsed returns an account's current storage usage in bytes (the same
// aggregate the quota service enforces against), for the admin user view.
func (s *Service) StorageUsed(ctx context.Context, userID uuid.UUID) (int64, error) {
	return s.repo.SumUserStorageUsage(ctx, userID)
}

// Stats is the instance-wide overview the admin dashboard renders as cards.
// Every field is a live, trivially-real aggregate over a committed column;
// there are deliberately NO period-over-period deltas — no time-series store
// exists to compute them (that is a W3 analytics dependency).
type Stats struct {
	// Users is the total number of accounts (active or not).
	Users int64
	// PublishedVideos is the count of public, published videos — the same total
	// NodeInfo advertises as local posts.
	PublishedVideos int64
	// MediaStoredBytes is the total stored bytes of every video file across all
	// accounts (originals, renditions, thumbnails).
	MediaStoredBytes int64
	// FederatedPeers is the number of distinct remote instances we have cached
	// actors from.
	FederatedPeers int64
	// Comments is the total number of local comments.
	Comments int64
}

// Stats aggregates the instance-wide admin-overview counts. Each is a single
// COUNT/SUM; any repository error is returned so the handler fails rather than
// serving misleading zeros.
func (s *Service) Stats(ctx context.Context) (Stats, error) {
	users, err := s.repo.CountUsers(ctx)
	if err != nil {
		return Stats{}, err
	}
	videos, err := s.repo.CountPublicVideos(ctx)
	if err != nil {
		return Stats{}, err
	}
	stored, err := s.repo.SumAllStorageUsage(ctx)
	if err != nil {
		return Stats{}, err
	}
	peers, err := s.repo.CountFederatedPeers(ctx)
	if err != nil {
		return Stats{}, err
	}
	comments, err := s.repo.CountComments(ctx)
	if err != nil {
		return Stats{}, err
	}
	return Stats{
		Users:            users,
		PublishedVideos:  videos,
		MediaStoredBytes: stored,
		FederatedPeers:   peers,
		Comments:         comments,
	}, nil
}
