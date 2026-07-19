// Package channel implements channel management for vidra-core: a channel is a
// publishing identity owned by a user. It is HTTP-agnostic and testable without
// a server.
package channel

import (
	"context"
	"errors"
	"strings"
	"time"

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
	// ErrMaxReached means the caller is at the per-user channel limit
	// (max_channels_per_user, config-parity W8; 0 = unlimited).
	ErrMaxReached = errors.New("channel: per-user limit reached")
)

// Repository is the data access the channel service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	CreateChannel(ctx context.Context, arg sqlcgen.CreateChannelParams) (sqlcgen.Channel, error)
	GetChannelByHandle(ctx context.Context, lowerHandle string) (sqlcgen.Channel, error)
	ListChannelsByOwner(ctx context.Context, ownerID uuid.UUID) ([]sqlcgen.Channel, error)
	UpdateChannel(ctx context.Context, arg sqlcgen.UpdateChannelParams) (sqlcgen.Channel, error)
	DeleteChannel(ctx context.Context, id uuid.UUID) error

	FollowChannel(ctx context.Context, arg sqlcgen.FollowChannelParams) (int64, error)
	UnfollowChannel(ctx context.Context, arg sqlcgen.UnfollowChannelParams) error
	CountChannelFollowers(ctx context.Context, channelID uuid.UUID) (int64, error)
	ListFollowedChannels(ctx context.Context, arg sqlcgen.ListFollowedChannelsParams) ([]sqlcgen.ListFollowedChannelsRow, error)
}

// Service holds the channel application logic.
type Service struct {
	repo Repository
	// maxPerUserFn, when set, is the runtime per-user channel cap
	// (max_channels_per_user, config-parity W8), resolved per Create.
	// <= 0 = unlimited (the default, matching the shipped behaviour).
	maxPerUserFn func() int64
}

// Option customises the Service.
type Option func(*Service)

// WithMaxPerUserFunc wires the dynamic per-user channel cap
// (max_channels_per_user, config-parity W8): f is resolved per Create so an
// admin can retune the limit without a restart. <= 0 = unlimited. Existing
// channels above a newly lowered cap are untouched — the cap only refuses NEW
// creations.
func WithMaxPerUserFunc(f func() int64) Option {
	return func(s *Service) { s.maxPerUserFn = f }
}

// NewService builds the channel service.
func NewService(repo Repository, opts ...Option) *Service {
	s := &Service{repo: repo}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// CreateInput is validated, normalized channel-creation data.
type CreateInput struct {
	Handle      string
	DisplayName string
	Description string
}

// Create makes a new channel owned by ownerID. The per-user cap (when one is
// wired; 0 = unlimited) is counted-and-refused first — ErrMaxReached. Handle
// uniqueness is enforced by the database; a violation maps to ErrConflict.
func (s *Service) Create(ctx context.Context, ownerID uuid.UUID, in CreateInput) (sqlcgen.Channel, error) {
	if s.maxPerUserFn != nil {
		if max := s.maxPerUserFn(); max > 0 {
			existing, err := s.repo.ListChannelsByOwner(ctx, ownerID)
			if err != nil {
				return sqlcgen.Channel{}, err
			}
			if int64(len(existing)) >= max {
				return sqlcgen.Channel{}, ErrMaxReached
			}
		}
	}
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
	// ActivitypubEnabled/AtprotoEnabled are the per-channel protocol
	// distribution flags (migration 0096). nil leaves the flag unchanged.
	ActivitypubEnabled *bool
	AtprotoEnabled     *bool
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
		ID:                 ch.ID,
		DisplayName:        trimPtr(in.DisplayName),
		Description:        trimPtr(in.Description),
		ActivitypubEnabled: in.ActivitypubEnabled,
		AtprotoEnabled:     in.AtprotoEnabled,
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

// Follow makes followerID follow the channel with the given handle. It is
// idempotent (following twice is a no-op). It returns the followed channel and
// whether this was a new follow (false when already following), so callers can
// fire a side effect — e.g. a notification — only on a new follow. An unknown
// handle → ErrNotFound.
func (s *Service) Follow(ctx context.Context, followerID uuid.UUID, handle string) (sqlcgen.Channel, bool, error) {
	ch, err := s.GetByHandle(ctx, handle)
	if err != nil {
		return sqlcgen.Channel{}, false, err
	}
	rows, err := s.repo.FollowChannel(ctx, sqlcgen.FollowChannelParams{
		FollowerID: followerID,
		ChannelID:  ch.ID,
	})
	if err != nil {
		return sqlcgen.Channel{}, false, err
	}
	return ch, rows > 0, nil
}

// Unfollow removes followerID's follow of the channel. Idempotent; unknown
// handle → ErrNotFound.
func (s *Service) Unfollow(ctx context.Context, followerID uuid.UUID, handle string) error {
	ch, err := s.GetByHandle(ctx, handle)
	if err != nil {
		return err
	}
	return s.repo.UnfollowChannel(ctx, sqlcgen.UnfollowChannelParams{
		FollowerID: followerID,
		ChannelID:  ch.ID,
	})
}

// FollowerCount returns how many followers a channel has.
func (s *Service) FollowerCount(ctx context.Context, channelID uuid.UUID) (int64, error) {
	return s.repo.CountChannelFollowers(ctx, channelID)
}

// Followed is a channel the caller follows, paired with the channel's total
// follower count and when the caller followed it.
type Followed struct {
	Channel       sqlcgen.Channel
	FollowerCount int64
	FollowedAt    time.Time
}

// ListFollowed returns the local channels followerID follows (the "FOLLOWING"
// list), most recently followed first, paginated. limit is clamped to [1,100]
// and offset to >= 0 by the caller.
func (s *Service) ListFollowed(ctx context.Context, followerID uuid.UUID, limit, offset int32) ([]Followed, error) {
	rows, err := s.repo.ListFollowedChannels(ctx, sqlcgen.ListFollowedChannelsParams{
		FollowerID: followerID,
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Followed, 0, len(rows))
	for _, r := range rows {
		out = append(out, Followed{
			Channel: sqlcgen.Channel{
				ID:                 r.ID,
				OwnerID:            r.OwnerID,
				Handle:             r.Handle,
				DisplayName:        r.DisplayName,
				Description:        r.Description,
				ActivitypubEnabled: r.ActivitypubEnabled,
				AtprotoEnabled:     r.AtprotoEnabled,
				CreatedAt:          r.CreatedAt,
				UpdatedAt:          r.UpdatedAt,
			},
			FollowerCount: r.FollowerCount,
			FollowedAt:    r.FollowedAt,
		})
	}
	return out, nil
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
