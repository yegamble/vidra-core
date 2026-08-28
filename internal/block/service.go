// Package block implements user-to-user blocks for vidra-core: a user blocks
// another account to cut off direct interaction. Unlike a mute (which
// one-directionally hides the muted account's content — see internal/mute), a
// block is enforced symmetrically: if EITHER user has blocked the other, direct
// messaging between them is refused in both directions. This package owns the
// block model and management (block / unblock / list) plus the symmetric
// IsBlockedBetween check that other features (messaging today) consult. It is
// HTTP-agnostic and testable without a server.
package block

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrCannotBlockSelf means blocker and target are the same account.
	ErrCannotBlockSelf = errors.New("block: cannot block yourself")
	// ErrUserNotFound means the blocked target account does not exist.
	ErrUserNotFound = errors.New("block: target user not found")
)

// Repository is the data access the block service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	BlockUser(ctx context.Context, arg sqlcgen.BlockUserParams) (int64, error)
	UnblockUser(ctx context.Context, arg sqlcgen.UnblockUserParams) (int64, error)
	ListBlockedUsers(ctx context.Context, arg sqlcgen.ListBlockedUsersParams) ([]sqlcgen.ListBlockedUsersRow, error)
	CountBlockedUsers(ctx context.Context, blockerID uuid.UUID) (int64, error)
	IsBlockedBetween(ctx context.Context, arg sqlcgen.IsBlockedBetweenParams) (bool, error)
}

// Service holds the block application logic.
type Service struct {
	repo Repository
}

// NewService builds the block service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// BlockedUser is a blocked account with its display identity, for list responses.
type BlockedUser struct {
	UserID      uuid.UUID
	Username    string
	DisplayName string
	BlockedAt   time.Time
}

// Block records that blockerID blocks blockedID. Idempotent. A self-block →
// ErrCannotBlockSelf; an unknown target → ErrUserNotFound.
func (s *Service) Block(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	if blockerID == blockedID {
		return ErrCannotBlockSelf
	}
	_, err := s.repo.BlockUser(ctx, sqlcgen.BlockUserParams{BlockerID: blockerID, BlockedID: blockedID})
	if pgconv.SQLState(err) == pgconv.SQLStateForeignKeyViolation { // foreign-key violation: no such user
		return ErrUserNotFound
	}
	return err
}

// Unblock lifts blockerID's block of blockedID (idempotent: unblocking a
// not-blocked account is a no-op).
func (s *Service) Unblock(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	_, err := s.repo.UnblockUser(ctx, sqlcgen.UnblockUserParams{BlockerID: blockerID, BlockedID: blockedID})
	return err
}

// List returns the accounts blockerID has blocked, newest block first. The
// caller clamps limit/offset.
func (s *Service) List(ctx context.Context, blockerID uuid.UUID, limit, offset int32) ([]BlockedUser, int64, error) {
	rows, err := s.repo.ListBlockedUsers(ctx, sqlcgen.ListBlockedUsersParams{
		BlockerID:    blockerID,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountBlockedUsers(ctx, blockerID)
	if err != nil {
		return nil, 0, err
	}
	out := make([]BlockedUser, 0, len(rows))
	for _, r := range rows {
		out = append(out, BlockedUser{
			UserID:      r.BlockedID,
			Username:    r.Username,
			DisplayName: r.DisplayName,
			BlockedAt:   r.CreatedAt,
		})
	}
	return out, total, nil
}

// IsBlockedBetween reports whether either user has blocked the other. Used by
// messaging to refuse a conversation/message in either direction. Satisfies the
// messaging.Blocker interface.
func (s *Service) IsBlockedBetween(ctx context.Context, a, b uuid.UUID) (bool, error) {
	return s.repo.IsBlockedBetween(ctx, sqlcgen.IsBlockedBetweenParams{BlockerID: a, BlockedID: b})
}
