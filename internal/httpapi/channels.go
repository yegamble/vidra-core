package httpapi

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// handleRe constrains channel handles to a URL-safe, federation-friendly shape:
// 3–30 chars of letters, digits, underscore.
var handleRe = regexp.MustCompile(`^[A-Za-z0-9_]{3,30}$`)

// createChannelRequest is the POST /api/v1/channels body.
type createChannelRequest struct {
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

func (r createChannelRequest) Validate() []FieldError {
	var fes []FieldError
	if !handleRe.MatchString(strings.TrimSpace(r.Handle)) {
		fes = append(fes, FieldError{Field: "handle", Message: "must be 3–30 chars: letters, digits, or underscore"})
	}
	switch n := len(strings.TrimSpace(r.DisplayName)); {
	case n == 0:
		fes = append(fes, FieldError{Field: "display_name", Message: "is required"})
	case n > 50:
		fes = append(fes, FieldError{Field: "display_name", Message: "must be at most 50 characters"})
	}
	if len(r.Description) > 1000 {
		fes = append(fes, FieldError{Field: "description", Message: "must be at most 1000 characters"})
	}
	return fes
}

// channelView is the public projection of a channel.
type channelView struct {
	ID            string    `json:"id"`
	OwnerID       string    `json:"owner_id"`
	Handle        string    `json:"handle"`
	DisplayName   string    `json:"display_name"`
	Description   string    `json:"description"`
	FollowerCount int64     `json:"follower_count"`
	CreatedAt     time.Time `json:"created_at"`
	// HasAvatar/HasBanner are set when the profile-image service is wired
	// (omitted otherwise); when true the image is served at
	// GET /channels/{handle}/avatar | /banner.
	HasAvatar *bool `json:"has_avatar,omitempty"`
	HasBanner *bool `json:"has_banner,omitempty"`
	// ActivitypubEnabled/AtprotoEnabled are the per-channel protocol
	// distribution flags (migration 0096): whether the channel publishes over
	// ActivityPub / ATProto. Owner-editable via PATCH.
	ActivitypubEnabled bool `json:"activitypub_enabled"`
	AtprotoEnabled     bool `json:"atproto_enabled"`
	// AtprotoActive is the effective ATProto distribution status: atproto_enabled
	// AND the channel owner has a linked Bluesky account AND the instance ATProto
	// extension is on. Computed only where cheap (single GET, /me/channels);
	// omitted (nil) in bulk listings and when the extension is not wired.
	AtprotoActive *bool `json:"atproto_active,omitempty"`
}

func newChannelView(c sqlcgen.Channel, followerCount int64) channelView {
	return channelView{
		ID:                 c.ID.String(),
		OwnerID:            c.OwnerID.String(),
		Handle:             c.Handle,
		DisplayName:        c.DisplayName,
		Description:        c.Description,
		FollowerCount:      followerCount,
		CreatedAt:          c.CreatedAt,
		ActivitypubEnabled: c.ActivitypubEnabled,
		AtprotoEnabled:     c.AtprotoEnabled,
	}
}

// channelListResponse wraps a list of channels.
type channelListResponse struct {
	Channels []channelView `json:"channels"`
}

// followedChannelView is a channel the caller follows, with the follow time
// added to the standard channel projection.
type followedChannelView struct {
	channelView
	FollowedAt time.Time `json:"followed_at"`
}

// followedChannelsResponse is a page of the caller's followed channels.
type followedChannelsResponse struct {
	Channels []followedChannelView `json:"channels"`
	Limit    int                   `json:"limit"`
	Offset   int                   `json:"offset"`
}

// handleCreateChannel creates a channel owned by the authenticated user. 422
// when the caller is at the operator's per-user channel limit
// (max_channels_per_user, W8; 0 = unlimited).
func (s *Server) handleCreateChannel(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var in createChannelRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ch, err := s.channelsvc.Create(c.Request().Context(), userID, channel.CreateInput{
		Handle:      in.Handle,
		DisplayName: in.DisplayName,
		Description: in.Description,
	})
	if err != nil {
		if errors.Is(err, channel.ErrConflict) {
			return echo.NewHTTPError(http.StatusConflict, "channel handle already taken")
		}
		if errors.Is(err, channel.ErrMaxReached) {
			return echo.NewHTTPError(http.StatusUnprocessableEntity,
				"you have reached the maximum number of channels allowed on this instance")
		}
		return err
	}
	// A just-created channel has no followers yet.
	view := s.channelViewFor(c.Request().Context(), ch, 0)
	view.AtprotoActive = s.atprotoActive(c.Request().Context(), ch)
	return c.JSON(http.StatusCreated, view)
}

// handleListMyChannels lists the authenticated user's channels.
func (s *Server) handleListMyChannels(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	ctx := c.Request().Context()
	chans, err := s.channelsvc.ListOwn(ctx, userID)
	if err != nil {
		return err
	}
	// All of these channels share one owner (the caller), so the ATProto
	// owner-linked probe is identical for each — resolve it once to keep
	// /me/channels O(1) in ATProto lookups rather than O(channels).
	atActive := s.atprotoActiveForOwner(ctx, userID)
	views := make([]channelView, 0, len(chans))
	for _, ch := range chans {
		count, err := s.channelsvc.FollowerCount(ctx, ch.ID)
		if err != nil {
			return err
		}
		view := s.channelViewFor(ctx, ch, count)
		if atActive != nil {
			active := *atActive && ch.AtprotoEnabled
			view.AtprotoActive = &active
		}
		views = append(views, view)
	}
	return c.JSON(http.StatusOK, channelListResponse{Channels: views})
}

// handleListFollowedChannels lists the LOCAL channels the authenticated user
// follows (the "FOLLOWING" list), most recently followed first. Paginated via
// ?limit (1–100, default 20) and ?offset.
func (s *Server) handleListFollowedChannels(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	ctx := c.Request().Context()
	followed, err := s.channelsvc.ListFollowed(ctx, userID, int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]followedChannelView, 0, len(followed))
	for _, f := range followed {
		views = append(views, followedChannelView{
			channelView: s.channelViewFor(ctx, f.Channel, f.FollowerCount),
			FollowedAt:  f.FollowedAt,
		})
	}
	return c.JSON(http.StatusOK, followedChannelsResponse{Channels: views, Limit: limit, Offset: offset})
}

// updateChannelRequest is the PATCH /api/v1/channels/{handle} body. Fields are
// optional; only those present are changed. The handle itself is immutable.
type updateChannelRequest struct {
	DisplayName *string `json:"display_name"`
	Description *string `json:"description"`
	// Per-channel protocol distribution flags (migration 0096). Owner-only, as
	// with the rest of PATCH. Absent = unchanged.
	ActivitypubEnabled *bool `json:"activitypub_enabled"`
	AtprotoEnabled     *bool `json:"atproto_enabled"`
}

func (r updateChannelRequest) Validate() []FieldError {
	var fes []FieldError
	if r.DisplayName == nil && r.Description == nil && r.ActivitypubEnabled == nil && r.AtprotoEnabled == nil {
		fes = append(fes, FieldError{Field: "display_name", Message: "at least one of display_name, description, activitypub_enabled, atproto_enabled is required"})
		return fes
	}
	if r.DisplayName != nil {
		switch n := len(strings.TrimSpace(*r.DisplayName)); {
		case n == 0:
			fes = append(fes, FieldError{Field: "display_name", Message: "must not be blank"})
		case n > 50:
			fes = append(fes, FieldError{Field: "display_name", Message: "must be at most 50 characters"})
		}
	}
	if r.Description != nil && len(*r.Description) > 1000 {
		fes = append(fes, FieldError{Field: "description", Message: "must be at most 1000 characters"})
	}
	return fes
}

// handleUpdateChannel updates a channel owned by the authenticated user.
func (s *Server) handleUpdateChannel(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var in updateChannelRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	ch, err := s.channelsvc.Update(ctx, userID, c.Param("handle"), channel.UpdateInput{
		DisplayName:        in.DisplayName,
		Description:        in.Description,
		ActivitypubEnabled: in.ActivitypubEnabled,
		AtprotoEnabled:     in.AtprotoEnabled,
	})
	if err != nil {
		return channelError(err)
	}
	// Search: refresh the denormalized channel fields on its docs (search-service
	// W4). Best-effort.
	s.searchEvents.EnqueueChannelUpsert(ctx, ch.ID, ch.Handle, ch.DisplayName, ch.OwnerID)
	count, err := s.channelsvc.FollowerCount(ctx, ch.ID)
	if err != nil {
		return err
	}
	view := s.channelViewFor(ctx, ch, count)
	view.AtprotoActive = s.atprotoActive(ctx, ch)
	return c.JSON(http.StatusOK, view)
}

// handleDeleteChannel deletes a channel owned by the authenticated user.
func (s *Server) handleDeleteChannel(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	ctx := c.Request().Context()
	// Resolve the id before deletion so the search suppression targets it.
	var chID uuid.UUID
	if ch, gerr := s.channelsvc.GetByHandle(ctx, c.Param("handle")); gerr == nil {
		chID = ch.ID
	}
	if err := s.channelsvc.Delete(ctx, userID, c.Param("handle")); err != nil {
		return channelError(err)
	}
	s.audit(c, observability.ActionChannelDelete, observability.ResultSuccess, userID.String(), c.Param("handle"))
	// Search: suppress the channel's docs (search-service W4). Best-effort.
	if chID != uuid.Nil {
		s.searchEvents.EnqueueChannelDelete(ctx, chID)
	}
	return c.NoContent(http.StatusNoContent)
}

// channelError maps channel service sentinels to HTTP error envelopes.
func channelError(err error) error {
	switch {
	case errors.Is(err, channel.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "channel not found")
	case errors.Is(err, channel.ErrForbidden):
		return echo.NewHTTPError(http.StatusForbidden, "you do not own this channel")
	case errors.Is(err, channel.ErrConflict):
		return echo.NewHTTPError(http.StatusConflict, "channel handle already taken")
	default:
		return err
	}
}

// handleGetChannel returns a channel by its public handle. No auth required.
func (s *Server) handleGetChannel(c echo.Context) error {
	ctx := c.Request().Context()
	ch, err := s.channelsvc.GetByHandle(ctx, c.Param("handle"))
	if err != nil {
		if errors.Is(err, channel.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "channel not found")
		}
		return err
	}
	count, err := s.channelsvc.FollowerCount(ctx, ch.ID)
	if err != nil {
		return err
	}
	view := s.channelViewFor(ctx, ch, count)
	view.AtprotoActive = s.atprotoActive(ctx, ch)
	return c.JSON(http.StatusOK, view)
}

// atprotoActive computes a channel's effective ATProto distribution status for
// the studio Distribution surface: the instance ATProto extension is on, the
// channel opted in (atproto_enabled), AND the channel owner has a linked
// Bluesky account. Returns nil — omitting the field — when the ATProto
// extension is not wired/enabled on this instance (the studio hides the row).
func (s *Server) atprotoActive(ctx context.Context, ch sqlcgen.Channel) *bool {
	owner := s.atprotoActiveForOwner(ctx, ch.OwnerID)
	if owner == nil {
		return nil
	}
	active := *owner && ch.AtprotoEnabled
	return &active
}

// atprotoActiveForOwner reports whether ATProto cross-posting is live for the
// given owner: extension enabled on the instance AND the owner has a linked
// Bluesky account. Returns nil when the extension is not wired/enabled (the
// atproto_active field is then omitted). Callers combine it with a channel's
// own atproto_enabled flag.
func (s *Server) atprotoActiveForOwner(ctx context.Context, ownerID uuid.UUID) *bool {
	if s.atprotosvc == nil || !s.atprotosvc.Enabled() {
		return nil
	}
	linked := false
	if _, err := s.atprotosvc.Status(ctx, ownerID); err == nil {
		linked = true
	}
	return &linked
}

// handleFollowChannel makes the authenticated user follow a channel. Idempotent.
func (s *Server) handleFollowChannel(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	ctx := c.Request().Context()
	ch, created, err := s.channelsvc.Follow(ctx, userID, c.Param("handle"))
	if err != nil {
		return channelError(err)
	}
	// Notify the channel owner of a genuinely new follow (best-effort: never the
	// caller's concern; skipped when no notifier is wired or you follow your own).
	if created && s.notifsvc != nil {
		if nerr := s.notifsvc.NotifyFollow(ctx, ch.OwnerID, userID, ch.ID); nerr != nil {
			s.logger.WarnContext(ctx, "notify follow failed", "error", nerr, "channel_id", ch.ID)
		}
	}
	return c.NoContent(http.StatusNoContent)
}

// handleUnfollowChannel removes the authenticated user's follow. Idempotent.
func (s *Server) handleUnfollowChannel(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	if err := s.channelsvc.Unfollow(c.Request().Context(), userID, c.Param("handle")); err != nil {
		return channelError(err)
	}
	return c.NoContent(http.StatusNoContent)
}
