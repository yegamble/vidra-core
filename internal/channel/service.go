// Package channel implements channel management for vidra-core: a channel is a
// publishing identity owned by a user. It is HTTP-agnostic and testable without
// a server.
package channel

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrConflict means the handle is already taken.
	ErrConflict = errors.New("channel: handle already taken")
	// ErrNotFound means no channel matches the lookup.
	ErrNotFound = errors.New("channel: not found")
	// ErrForbidden means the caller does not own the channel.
	ErrForbidden = errors.New("channel: not owner")
)

// Repository is the data access the channel service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	CreateChannel(ctx context.Context, arg sqlcgen.CreateChannelParams) (sqlcgen.Channel, error)
	GetChannelByHandle(ctx context.Context, lowerHandle string) (sqlcgen.Channel, error)
	ListChannelsByOwner(ctx context.Context, ownerID uuid.UUID) ([]sqlcgen.Channel, error)
	UpdateChannel(ctx context.Context, arg sqlcgen.UpdateChannelParams) (sqlcgen.Channel, error)
	DeleteChannel(ctx context.Context, id uuid.UUID) error
}

// Service holds the channel application logic.
type Service struct {
	repo Repository
}

// NewService builds the channel service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// CreateInput is validated, normalized channel-creation data.
type CreateInput struct {
	Handle      string
	DisplayName string
	Description string
}

// Create makes a new channel owned by ownerID. Handle uniqueness is enforced by
// the database; a violation maps to ErrConflict.
func (s *Service) Create(ctx context.Context, ownerID uuid.UUID, in CreateInput) (sqlcgen.Channel, error) {
	ch, err := s.repo.CreateChannel(ctx, sqlcgen.CreateChannelParams{
		OwnerID:     ownerID,
		Handle:      strings.TrimSpace(in.Handle),
		DisplayName: strings.TrimSpace(in.DisplayName),
		Description: strings.TrimSpace(in.Description),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return sqlcgen.Channel{}, ErrConflict
		}
		return sqlcgen.Channel{}, err
	}
	return ch, nil
}

// GetByHandle returns the channel with the given (case-insensitive) handle.
func (s *Service) GetByHandle(ctx context.Context, handle string) (sqlcgen.Channel, error) {
	ch, err := s.repo.GetChannelByHandle(ctx, strings.TrimSpace(handle))
	if err != nil {
		return sqlcgen.Channel{}, ErrNotFound
	}
	return ch, nil
}

// ListOwn returns all channels owned by the given user, oldest first.
func (s *Service) ListOwn(ctx context.Context, ownerID uuid.UUID) ([]sqlcgen.Channel, error) {
	return s.repo.ListChannelsByOwner(ctx, ownerID)
}

// UpdateInput is a partial channel update: nil fields are left unchanged.
type UpdateInput struct {
	DisplayName *string
	Description *string
}

// Update changes a channel's mutable fields. Only the owner may update; a
// non-owner gets ErrForbidden and an unknown handle gets ErrNotFound. The handle
// itself is immutable.
func (s *Service) Update(ctx context.Context, ownerID uuid.UUID, handle string, in UpdateInput) (sqlcgen.Channel, error) {
	ch, err := s.GetByHandle(ctx, handle)
	if err != nil {
		return sqlcgen.Channel{}, err
	}
	if ch.OwnerID != ownerID {
		return sqlcgen.Channel{}, ErrForbidden
	}
	return s.repo.UpdateChannel(ctx, sqlcgen.UpdateChannelParams{
		ID:          ch.ID,
		DisplayName: trimPtr(in.DisplayName),
		Description: trimPtr(in.Description),
	})
}

// Delete removes a channel. Only the owner may delete; non-owner → ErrForbidden,
// unknown handle → ErrNotFound.
func (s *Service) Delete(ctx context.Context, ownerID uuid.UUID, handle string) error {
	ch, err := s.GetByHandle(ctx, handle)
	if err != nil {
		return err
	}
	if ch.OwnerID != ownerID {
		return ErrForbidden
	}
	return s.repo.DeleteChannel(ctx, ch.ID)
}

// trimPtr trims a non-nil string pointer's value, leaving nil untouched so a
// COALESCE update skips the column.
func trimPtr(p *string) *string {
	if p == nil {
		return nil
	}
	t := strings.TrimSpace(*p)
	return &t
}

// isUniqueViolation reports whether err is a PostgreSQL unique-constraint
// violation (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
