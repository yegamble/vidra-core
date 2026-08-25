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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

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
	// ErrUserNotFound means the invite target is not an existing local user (404).
	ErrUserNotFound = errors.New("channel: user not found")
	// ErrAlreadyMember means the invite target already manages the channel — it
	// is the owner or an existing member (409).
	ErrAlreadyMember = errors.New("channel: already a member or owner")
	// ErrInvalidNotificationSetting means a bell mode outside the supported set
	// was requested (422).
	ErrInvalidNotificationSetting = errors.New("channel: invalid notification setting")
)

// Per-channel notification bell modes (migration 0101). NotifyAll is the default
// a new follow gets: the follower is told about every new public video from the
// channel. NotifyNone mutes the bell without dropping the subscription — the
// channel stays in the follower's feed, it just stops generating notifications.
// YouTube's "personalized" middle mode is deliberately absent: there is no
// personalisation engine behind it here, so offering it would be a lie.
const (
	NotifyAll  = "all"
	NotifyNone = "none"
)

// validNotificationSetting reports whether s is a supported bell mode.
func validNotificationSetting(s string) bool {
	return s == NotifyAll || s == NotifyNone
}

// Member roles. The owner is implicit (channels.owner_id) and never stored in
// channel_members; members carry an explicit role, today only "editor".
const (
	RoleOwner  = "owner"
	RoleEditor = "editor"
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
	GetFollowNotificationSetting(ctx context.Context, arg sqlcgen.GetFollowNotificationSettingParams) (string, error)
	SetFollowNotificationSetting(ctx context.Context, arg sqlcgen.SetFollowNotificationSettingParams) (int64, error)
	CountChannelFollowers(ctx context.Context, channelID uuid.UUID) (int64, error)
	CountFollowersByOwner(ctx context.Context, ownerID uuid.UUID) ([]sqlcgen.CountFollowersByOwnerRow, error)
	ListFollowedChannels(ctx context.Context, arg sqlcgen.ListFollowedChannelsParams) ([]sqlcgen.ListFollowedChannelsRow, error)
	CountFollowedChannels(ctx context.Context, followerID uuid.UUID) (int64, error)

	// Collaborators (migration 0097).
	GetUserByUsername(ctx context.Context, lowerUsername string) (sqlcgen.User, error)
	AddChannelMember(ctx context.Context, arg sqlcgen.AddChannelMemberParams) (sqlcgen.ChannelMember, error)
	GetChannelMember(ctx context.Context, arg sqlcgen.GetChannelMemberParams) (sqlcgen.ChannelMember, error)
	DeleteChannelMember(ctx context.Context, arg sqlcgen.DeleteChannelMemberParams) (int64, error)
	ListChannelMembers(ctx context.Context, arg sqlcgen.ListChannelMembersParams) ([]sqlcgen.ListChannelMembersRow, error)
	CountChannelMembers(ctx context.Context, channelID uuid.UUID) (int64, error)
	IsChannelManager(ctx context.Context, arg sqlcgen.IsChannelManagerParams) (bool, error)
	ListChannelsForMember(ctx context.Context, userID uuid.UUID) ([]sqlcgen.ListChannelsForMemberRow, error)
	ListManagedChannels(ctx context.Context, arg sqlcgen.ListManagedChannelsParams) ([]sqlcgen.ListManagedChannelsRow, error)
	CountManagedChannels(ctx context.Context, userID uuid.UUID) (int64, error)
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

// SetFollowNotification sets the caller's bell mode for a channel they follow
// (migration 0101). The bell is a property of the subscription, so a caller who
// does not follow the channel gets ErrNotFound — exactly as an unknown handle
// does — rather than a silently-stored preference for a channel they left. An
// unsupported mode is refused before any write with
// ErrInvalidNotificationSetting.
func (s *Service) SetFollowNotification(ctx context.Context, followerID uuid.UUID, handle, setting string) error {
	if !validNotificationSetting(setting) {
		return ErrInvalidNotificationSetting
	}
	ch, err := s.GetByHandle(ctx, handle)
	if err != nil {
		return err
	}
	rows, err := s.repo.SetFollowNotificationSetting(ctx, sqlcgen.SetFollowNotificationSettingParams{
		FollowerID:          followerID,
		ChannelID:           ch.ID,
		NotificationSetting: setting,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// FollowState reports whether followerID follows channelID and, when they do,
// their bell mode for it. A non-follower gets (false, "", nil) — not following
// is an ordinary answer, not an error — so callers can decorate a channel view
// with the caller's relationship in one lookup.
func (s *Service) FollowState(ctx context.Context, followerID, channelID uuid.UUID) (bool, string, error) {
	setting, err := s.repo.GetFollowNotificationSetting(ctx, sqlcgen.GetFollowNotificationSettingParams{
		FollowerID: followerID,
		ChannelID:  channelID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, "", nil
		}
		return false, "", err
	}
	return true, setting, nil
}

// FollowerCount returns how many followers a channel has.
func (s *Service) FollowerCount(ctx context.Context, channelID uuid.UUID) (int64, error) {
	return s.repo.CountChannelFollowers(ctx, channelID)
}

// FollowerCountsByOwner returns follower counts for every channel ownerID owns,
// keyed by channel id, in one grouped query — used by the account stats rollup
// (GET /me/stats) to attach per-channel and total follower counts without an
// N-per-channel fan-out. Channels with no followers are present with a 0.
func (s *Service) FollowerCountsByOwner(ctx context.Context, ownerID uuid.UUID) (map[uuid.UUID]int64, error) {
	rows, err := s.repo.CountFollowersByOwner(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]int64, len(rows))
	for _, r := range rows {
		out[r.ChannelID] = r.Followers
	}
	return out, nil
}

// Followed is a channel the caller follows, paired with the channel's total
// follower count, when the caller followed it, and the caller's bell mode for
// it (migration 0101) so the FOLLOWING list renders bell state without a
// per-row request.
type Followed struct {
	Channel             sqlcgen.Channel
	FollowerCount       int64
	FollowedAt          time.Time
	NotificationSetting string
}

// ListFollowed returns the local channels followerID follows (the "FOLLOWING"
// list), most recently followed first, paginated. limit is clamped to [1,100]
// and offset to >= 0 by the caller.
func (s *Service) ListFollowed(ctx context.Context, followerID uuid.UUID, limit, offset int32) ([]Followed, int64, error) {
	rows, err := s.repo.ListFollowedChannels(ctx, sqlcgen.ListFollowedChannelsParams{
		FollowerID: followerID,
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountFollowedChannels(ctx, followerID)
	if err != nil {
		return nil, 0, err
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
			FollowerCount:       r.FollowerCount,
			FollowedAt:          r.FollowedAt,
			NotificationSetting: r.NotificationSetting,
		})
	}
	return out, total, nil
}

// CanManageContent reports whether userID may manage channelID's content — the
// channel owner or an editor member. This is the single authorization primitive
// for every editor-accessible surface (upload/import, edit/delete/replace
// videos + thumbnails/captions/chapters, live streams, channel stats).
// Owner-only surfaces (channel PATCH/DELETE, avatar/banner, member management,
// protocol flags, sync) never consult it.
func (s *Service) CanManageContent(ctx context.Context, channelID, userID uuid.UUID) (bool, error) {
	return s.repo.IsChannelManager(ctx, sqlcgen.IsChannelManagerParams{
		ChannelID: channelID,
		UserID:    userID,
	})
}

// Member is a channel collaborator with the display fields the API surfaces.
type Member struct {
	UserID      uuid.UUID
	Username    string
	DisplayName string
	Role        string
	CreatedAt   time.Time
}

// ListMembers returns a channel's members. The owner and existing members may
// view the roster; anyone else gets ErrForbidden. Unknown handle → ErrNotFound.
func (s *Service) ListMembers(ctx context.Context, requesterID uuid.UUID, handle string, limit, offset int32) ([]Member, int64, error) {
	ch, err := s.GetByHandle(ctx, handle)
	if err != nil {
		return nil, 0, err
	}
	if ch.OwnerID != requesterID {
		manages, err := s.CanManageContent(ctx, ch.ID, requesterID)
		if err != nil {
			return nil, 0, err
		}
		if !manages {
			return nil, 0, ErrForbidden
		}
	}
	rows, err := s.repo.ListChannelMembers(ctx, sqlcgen.ListChannelMembersParams{
		ChannelID:    ch.ID,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountChannelMembers(ctx, ch.ID)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Member, 0, len(rows))
	for _, r := range rows {
		out = append(out, Member{
			UserID:      r.UserID,
			Username:    r.Username,
			DisplayName: r.DisplayName,
			Role:        r.Role,
			CreatedAt:   r.CreatedAt,
		})
	}
	return out, total, nil
}

// AddMember invites the local user identified by targetHandle as a member of
// the channel. Owner only (non-owner → ErrForbidden, unknown handle →
// ErrNotFound). The target must be an existing active local user
// (ErrUserNotFound otherwise) and must not already manage the channel — being
// the owner or an existing member is ErrAlreadyMember.
func (s *Service) AddMember(ctx context.Context, ownerID uuid.UUID, handle, targetHandle, role string) (Member, error) {
	ch, err := s.GetByHandle(ctx, handle)
	if err != nil {
		return Member{}, err
	}
	if ch.OwnerID != ownerID {
		return Member{}, ErrForbidden
	}
	if role == "" {
		role = RoleEditor
	}
	target, err := s.repo.GetUserByUsername(ctx, strings.TrimSpace(targetHandle))
	if err != nil {
		return Member{}, ErrUserNotFound
	}
	if target.ID == ch.OwnerID {
		return Member{}, ErrAlreadyMember // the owner already manages it
	}
	if _, err := s.repo.GetChannelMember(ctx, sqlcgen.GetChannelMemberParams{
		ChannelID: ch.ID,
		UserID:    target.ID,
	}); err == nil {
		return Member{}, ErrAlreadyMember
	}
	if _, err := s.repo.AddChannelMember(ctx, sqlcgen.AddChannelMemberParams{
		ChannelID: ch.ID,
		UserID:    target.ID,
		Role:      role,
		InvitedBy: pgtype.UUID{Bytes: ownerID, Valid: true},
	}); err != nil {
		if isUniqueViolation(err) {
			return Member{}, ErrAlreadyMember
		}
		return Member{}, err
	}
	return Member{
		UserID:      target.ID,
		Username:    target.Username,
		DisplayName: target.DisplayName,
		Role:        role,
	}, nil
}

// RemoveMember removes a member from the channel. Owner only (non-owner →
// ErrForbidden, unknown handle → ErrNotFound). Idempotent: removing someone who
// is not a member is a no-op success.
func (s *Service) RemoveMember(ctx context.Context, ownerID uuid.UUID, handle string, targetUserID uuid.UUID) error {
	ch, err := s.GetByHandle(ctx, handle)
	if err != nil {
		return err
	}
	if ch.OwnerID != ownerID {
		return ErrForbidden
	}
	_, err = s.repo.DeleteChannelMember(ctx, sqlcgen.DeleteChannelMemberParams{
		ChannelID: ch.ID,
		UserID:    targetUserID,
	})
	return err
}

// Managed pairs a channel with the caller's role on it (owner or editor).
type Managed struct {
	Channel sqlcgen.Channel
	Role    string
	// FollowerCount comes back with the row. It used to be fetched with one
	// CountChannelFollowers call PER channel in the HTTP handler — an N+1 that
	// grew with the user's channel count and could not be paginated away.
	FollowerCount int64
}

// ListManaged returns one page of every channel the user can act on: the ones
// they OWN (role "owner") plus the ones they are a member of (role "editor"),
// owned first, with the total. One UNION query replaces the previous Go-side
// merge of two unbounded lists, and carries each channel's follower count
// inline instead of a per-row count call. The caller clamps limit/offset.
func (s *Service) ListManaged(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]Managed, int64, error) {
	rows, err := s.repo.ListManagedChannels(ctx, sqlcgen.ListManagedChannelsParams{
		UserID:       userID,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountManagedChannels(ctx, userID)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Managed, 0, len(rows))
	for _, r := range rows {
		out = append(out, Managed{
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
			Role:          r.Role,
			FollowerCount: r.FollowerCount,
		})
	}
	return out, total, nil
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
