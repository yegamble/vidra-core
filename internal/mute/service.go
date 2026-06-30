// Package mute implements account mutes for vidra-core: a user mutes another
// account so that account's content becomes hidden from them. This package owns
// the mute model and management (mute / unmute / list); the filtering effect on
// each content surface (comments, feed) is applied by those surfaces. It is
// HTTP-agnostic and testable without a server.
package mute

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrCannotMuteSelf means muter and target are the same account.
	ErrCannotMuteSelf = errors.New("mute: cannot mute yourself")
	// ErrUserNotFound means the muted target account does not exist.
	ErrUserNotFound = errors.New("mute: target user not found")
)

// Repository is the data access the mute service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	MuteAccount(ctx context.Context, arg sqlcgen.MuteAccountParams) (int64, error)
	UnmuteAccount(ctx context.Context, arg sqlcgen.UnmuteAccountParams) (int64, error)
	ListMutedAccounts(ctx context.Context, arg sqlcgen.ListMutedAccountsParams) ([]sqlcgen.ListMutedAccountsRow, error)
}

// Service holds the mute application logic.
type Service struct {
	repo Repository
}

// NewService builds the mute service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// MutedAccount is a muted account with its display identity, for list responses.
type MutedAccount struct {
	UserID      uuid.UUID
	Username    string
	DisplayName string
	MutedAt     time.Time
}

// Mute records that muterID mutes mutedID. Idempotent. A self-mute →
// ErrCannotMuteSelf; an unknown target → ErrUserNotFound.
func (s *Service) Mute(ctx context.Context, muterID, mutedID uuid.UUID) error {
	if muterID == mutedID {
		return ErrCannotMuteSelf
	}
	_, err := s.repo.MuteAccount(ctx, sqlcgen.MuteAccountParams{MuterID: muterID, MutedID: mutedID})
	if sqlState(err) == "23503" { // foreign-key violation: no such user
		return ErrUserNotFound
	}
	return err
}

// Unmute lifts muterID's mute of mutedID (idempotent: unmuting a not-muted
// account is a no-op).
func (s *Service) Unmute(ctx context.Context, muterID, mutedID uuid.UUID) error {
	_, err := s.repo.UnmuteAccount(ctx, sqlcgen.UnmuteAccountParams{MuterID: muterID, MutedID: mutedID})
	return err
}

// List returns the accounts muterID has muted, newest mute first. The caller
// clamps limit/offset.
func (s *Service) List(ctx context.Context, muterID uuid.UUID, limit, offset int32) ([]MutedAccount, error) {
	rows, err := s.repo.ListMutedAccounts(ctx, sqlcgen.ListMutedAccountsParams{
		MuterID:      muterID,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]MutedAccount, 0, len(rows))
	for _, r := range rows {
		out = append(out, MutedAccount{
			UserID:      r.MutedID,
			Username:    r.Username,
			DisplayName: r.DisplayName,
			MutedAt:     r.CreatedAt,
		})
	}
	return out, nil
}

// sqlState returns the SQLSTATE code of a PostgreSQL error, "" otherwise.
func sqlState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}
