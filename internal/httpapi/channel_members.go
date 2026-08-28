package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// canManageChannelContent is the centralized authorization for every
// editor-accessible surface: it reports whether userID may manage channelID's
// content (the owner or an editor member — migration 0097). Owner-only surfaces
// (channel PATCH/DELETE, avatar/banner, member management, protocol flags, sync)
// never consult it; they keep their direct owner check. A nil channel service or
// any lookup error is treated as "not allowed" (fail closed).
func (s *Server) canManageChannelContent(ctx context.Context, userID, channelID uuid.UUID) bool {
	if s.channelsvc == nil {
		return false
	}
	ok, err := s.channelsvc.CanManageContent(ctx, channelID, userID)
	return err == nil && ok
}

// canManageVideo fetches a video and reports whether the caller may manage it —
// the owner of its channel or an editor member. ok is false when the video is
// unknown or the caller has no management right (callers answer 404 so existence
// is not leaked). It does NOT grant moderator/admin powers; those escapes stay
// role-based in the individual handlers. The returned row's OwnerID is the
// channel owner — the id the owner-gated video service methods expect, so an
// authorized editor's write executes as the channel owner (correct quota/
// attribution) once this check has passed.
func (s *Server) canManageVideo(ctx context.Context, userID, videoID uuid.UUID) (sqlcgen.GetVideoByIDRow, bool) {
	v, err := s.videosvc.GetByID(ctx, videoID)
	if err != nil {
		return sqlcgen.GetVideoByIDRow{}, false
	}
	if v.OwnerID == userID {
		return v, true
	}
	return v, s.canManageChannelContent(ctx, userID, v.ChannelID)
}

// channelMemberView is one collaborator in the channel members roster.
type channelMemberView struct {
	UserID      string    `json:"user_id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}

// channelMembersResponse wraps the members list.
type channelMembersResponse struct {
	Members []channelMemberView `json:"members"`
	pageMeta
}

func newChannelMemberView(m channel.Member) channelMemberView {
	return channelMemberView{
		UserID:      m.UserID.String(),
		Username:    m.Username,
		DisplayName: m.DisplayName,
		Role:        m.Role,
		CreatedAt:   m.CreatedAt,
	}
}

// addChannelMemberRequest is the POST /channels/{handle}/members body.
type addChannelMemberRequest struct {
	Handle string `json:"handle"`
	Role   string `json:"role"`
}

func (r addChannelMemberRequest) Validate() []FieldError {
	var fes []FieldError
	if strings.TrimSpace(r.Handle) == "" {
		fes = append(fes, FieldError{Field: "handle", Message: "is required"})
	}
	if r.Role != "" && r.Role != channel.RoleEditor {
		fes = append(fes, FieldError{Field: "role", Message: `must be "editor"`})
	}
	return fes
}

// memberError maps channel/member service sentinels to HTTP envelopes.
func memberError(err error) error {
	switch {
	case errors.Is(err, channel.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "channel not found")
	case errors.Is(err, channel.ErrForbidden):
		return echo.NewHTTPError(http.StatusForbidden, "you do not own this channel")
	case errors.Is(err, channel.ErrUserNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	case errors.Is(err, channel.ErrAlreadyMember):
		return echo.NewHTTPError(http.StatusConflict, "already a member or the channel owner")
	default:
		return err
	}
}

// handleListChannelMembers lists a channel's collaborators. Visible to the owner
// and to existing members; anyone else gets 403 (unknown handle → 404).
func (s *Server) handleListChannelMembers(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	members, total, err := s.channelsvc.ListMembers(c.Request().Context(), userID, c.Param("handle"), page.Limit32(), page.Offset32())
	if err != nil {
		return memberError(err)
	}
	views := make([]channelMemberView, 0, len(members))
	for _, m := range members {
		views = append(views, newChannelMemberView(m))
	}
	return c.JSON(http.StatusOK, channelMembersResponse{Members: views, pageMeta: page.meta(total)})
}

// handleAddChannelMember invites a local user as an editor of the channel. Owner
// only. 404 unknown channel or unknown target user; 409 when the target already
// manages the channel (owner or existing member).
func (s *Server) handleAddChannelMember(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in addChannelMemberRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	member, err := s.channelsvc.AddMember(c.Request().Context(), userID, c.Param("handle"), in.Handle, in.Role)
	if err != nil {
		return memberError(err)
	}
	return c.JSON(http.StatusCreated, newChannelMemberView(member))
}

// handleRemoveChannelMember removes a collaborator. Owner only; idempotent
// (removing a non-member is a 204). Unknown handle → 404.
func (s *Server) handleRemoveChannelMember(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	targetID, err := pathUUID(c, "userId", "member not found")
	if err != nil {
		return err
	}
	if err := s.channelsvc.RemoveMember(c.Request().Context(), userID, c.Param("handle"), targetID); err != nil {
		return memberError(err)
	}
	return c.NoContent(http.StatusNoContent)
}
