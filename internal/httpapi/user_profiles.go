package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/profileimage"
)

// publicUserProfileView is intentionally account-facing and secret-free. The
// publishing identities owned by the account remain first-class channels.
type publicUserProfileView struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Bio         string    `json:"bio"`
	CreatedAt   time.Time `json:"created_at"`
	HasAvatar   bool      `json:"has_avatar"`
	HasBanner   bool      `json:"has_banner"`
	// BlueskyHandle is the account's linked Bluesky/ATProto sign-in handle,
	// present only when the owner opted in (show_bluesky) AND a handle is on
	// file. Omitted otherwise.
	BlueskyHandle *string       `json:"bluesky_handle,omitempty"`
	Channels      []channelView `json:"channels"`
}

// handleGetUserProfile returns an opted-in public profile. A signed-in owner
// may also view their private profile as a preview; every other private,
// inactive, or unknown lookup returns the same 404.
func (s *Server) handleGetUserProfile(c echo.Context) error {
	ctx := c.Request().Context()
	username := strings.TrimSpace(c.Param("username"))

	var id uuid.UUID
	var displayName, bio string
	var createdAt time.Time
	// showBluesky gates exposing the linked Bluesky/ATProto handle; blueskyHandle
	// is the handle itself (nil when unlinked). Set from whichever branch resolves
	// the profile.
	var showBluesky bool
	var blueskyHandle *string
	if viewerID, _, ok := principalFromContext(c); ok {
		if me, err := s.authsvc.UserByID(ctx, viewerID); err == nil && strings.EqualFold(me.Username, username) {
			id, username, displayName, bio, createdAt = me.ID, me.Username, me.DisplayName, me.Bio, me.CreatedAt
			// Owner previewing their own profile: mirror what visitors see by
			// resolving the linked handle from the identity list (best-effort).
			showBluesky = me.ShowBluesky
			blueskyHandle = s.linkedBlueskyHandle(ctx, me.ID)
		}
	}
	if id == uuid.Nil {
		profile, err := s.authsvc.PublicProfileByUsername(ctx, username)
		if err != nil {
			if errors.Is(err, auth.ErrAccountNotFound) {
				return echo.NewHTTPError(http.StatusNotFound, "profile not found")
			}
			return err
		}
		id, username, displayName, bio, createdAt = profile.ID, profile.Username, profile.DisplayName, profile.Bio, profile.CreatedAt
		showBluesky, blueskyHandle = profile.ShowBluesky, profile.BlueskyHandle
	}

	channels, err := s.channelsvc.ListOwn(ctx, id)
	if err != nil {
		return err
	}
	views := make([]channelView, 0, len(channels))
	for _, ch := range channels {
		count, err := s.channelsvc.FollowerCount(ctx, ch.ID)
		if err != nil {
			return err
		}
		views = append(views, s.channelViewFor(ctx, ch, count))
	}

	view := publicUserProfileView{
		ID: id.String(), Username: username, DisplayName: displayName,
		Bio: bio, CreatedAt: createdAt, Channels: views,
	}
	if s.imagesvc != nil {
		view.HasAvatar = s.imagesvc.HasUserImage(ctx, id, profileimage.KindAvatar)
		view.HasBanner = s.imagesvc.HasUserImage(ctx, id, profileimage.KindBanner)
	}
	// Expose the Bluesky handle only on an explicit opt-in AND a non-empty handle.
	if showBluesky && blueskyHandle != nil {
		if h := strings.TrimSpace(*blueskyHandle); h != "" {
			view.BlueskyHandle = &h
		}
	}
	return c.JSON(http.StatusOK, view)
}

// handleListUserVideos lists an account's public videos across ALL of its
// channels (the profile Videos tab). Gated exactly like the profile itself: a
// private, inactive or unknown profile is the same 404, and a signed-in owner
// previewing their own profile resolves too. The videos are the public channel
// list's own visibility filter scoped by owner (ListPublicByOwner): public and
// published only, no moderator-blocked video, the per-viewer mute/block clause
// and the viewer's sensitive-content setting. Paginated like every list here.
func (s *Server) handleListUserVideos(c echo.Context) error {
	ctx := c.Request().Context()
	username := strings.TrimSpace(c.Param("username"))

	var ownerID uuid.UUID
	viewerID, _, viewerAuthed := principalFromContext(c)
	if viewerAuthed {
		if me, err := s.authsvc.UserByID(ctx, viewerID); err == nil && strings.EqualFold(me.Username, username) {
			ownerID = me.ID
		}
	}
	if ownerID == uuid.Nil {
		profile, err := s.authsvc.PublicProfileByUsername(ctx, username)
		if err != nil {
			if errors.Is(err, auth.ErrAccountNotFound) {
				return echo.NewHTTPError(http.StatusNotFound, "profile not found")
			}
			return err
		}
		ownerID = profile.ID
	}

	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.videosvc.ListPublicByOwner(ctx, ownerID, viewerID, viewerAuthed, s.effectiveHideSensitive(c), c.QueryParam("sort"), page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]videoView, 0, len(items))
	for _, it := range items {
		views = append(views, feedItemView(it))
	}
	s.attachIPFSPinned(ctx, views)
	return c.JSON(http.StatusOK, videoListResponse{Videos: views, pageMeta: page.meta(total)})
}

// linkedBlueskyHandle returns the account's linked ATProto (Bluesky) handle, or
// nil when none is linked or the OAuth service is not wired. Used only for the
// owner's own-profile preview; the public path reads the handle from the profile
// query's LEFT JOIN.
func (s *Server) linkedBlueskyHandle(ctx context.Context, userID uuid.UUID) *string {
	if s.oauthsvc == nil {
		return nil
	}
	idents, err := s.oauthsvc.Identities(ctx, userID)
	if err != nil {
		return nil
	}
	for _, id := range idents {
		if id.Provider == "atproto" && id.Handle != nil {
			return id.Handle
		}
	}
	return nil
}
