package httpapi

// Remote-video moderation surface (.ralph/specs/federation-remote-content.md
// §8): local reporting of federated remote videos plus the admin per-video
// hide (remote_video_blocks), mirroring the video_blocks endpoints. A blocked
// remote video vanishes from the subscriptions feed, search, the scope=all
// discovery feed, and the remote-watch detail endpoint.

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/moderation"
	"github.com/vidra/vidra-core/internal/observability"
)

// handleReportRemoteVideo files a report against an ingested remote video.
// Behind requireAuth. An unknown remote video is 404. Idempotent.
func (s *Server) handleReportRemoteVideo(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	remoteVideoID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "remote video not found")
	}
	var in createReportRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	reportID, err := s.moderationsvc.ReportRemoteVideo(c.Request().Context(), userID, remoteVideoID, strings.TrimSpace(in.Reason))
	if err != nil {
		if errors.Is(err, moderation.ErrInvalidTarget) {
			return echo.NewHTTPError(http.StatusNotFound, "remote video not found")
		}
		return err
	}
	s.notifyStaffOfReport(c, reportID, moderation.TargetRemoteVideo, strings.TrimSpace(in.Reason))
	return c.NoContent(http.StatusNoContent)
}

// handleBlockRemoteVideo hides a remote video from all local surfaces. Behind
// requireRole(admin, moderator). An unknown remote video is 404. Idempotent.
// Emits an audit event.
func (s *Server) handleBlockRemoteVideo(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "remote video not found")
	}
	var in blockVideoRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if err := s.moderationsvc.BlockRemoteVideo(c.Request().Context(), userID, id, strings.TrimSpace(in.Reason)); err != nil {
		if errors.Is(err, moderation.ErrRemoteVideoNotFound) {
			s.audit(c, observability.ActionRemoteVideoBlock, observability.ResultFailure, userID.String(), "not_found")
			return echo.NewHTTPError(http.StatusNotFound, "remote video not found")
		}
		return err
	}
	s.audit(c, observability.ActionRemoteVideoBlock, observability.ResultSuccess, userID.String(), "remote_video="+id.String())
	return c.NoContent(http.StatusNoContent)
}

// handleUnblockRemoteVideo lifts a remote video's block. Behind
// requireRole(admin, moderator). Idempotent. Emits an audit event.
func (s *Server) handleUnblockRemoteVideo(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "remote video not found")
	}
	if err := s.moderationsvc.UnblockRemoteVideo(c.Request().Context(), id); err != nil {
		return err
	}
	s.audit(c, observability.ActionRemoteVideoUnblock, observability.ResultSuccess, userID.String(), "remote_video="+id.String())
	return c.NoContent(http.StatusNoContent)
}

// blockedRemoteVideoView is the moderation block-list projection of a blocked
// remote video.
type blockedRemoteVideoView struct {
	RemoteVideoID string    `json:"remote_video_id"`
	Title         string    `json:"title"`
	ObjectURL     string    `json:"object_url"`
	WatchURL      string    `json:"watch_url"`
	ChannelHandle string    `json:"channel_handle"`
	Domain        string    `json:"domain"`
	Reason        string    `json:"reason"`
	BlockedBy     string    `json:"blocked_by,omitempty"`
	BlockedAt     time.Time `json:"blocked_at"`
}

// blockedRemoteVideoListResponse is the paginated remote-video block-list.
type blockedRemoteVideoListResponse struct {
	Videos []blockedRemoteVideoView `json:"videos"`
	pageMeta
}

// handleListBlockedRemoteVideos returns currently-blocked remote videos
// (newest block first). Behind requireRole(admin, moderator). Pagination via
// ?limit (1–100, default 20) and ?offset.
func (s *Server) handleListBlockedRemoteVideos(c echo.Context) error {
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.moderationsvc.ListBlockedRemote(c.Request().Context(), page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]blockedRemoteVideoView, 0, len(items))
	for _, it := range items {
		views = append(views, blockedRemoteVideoView{
			RemoteVideoID: it.RemoteVideoID.String(),
			Title:         it.Title,
			ObjectURL:     it.ObjectURL,
			WatchURL:      it.WatchURL,
			ChannelHandle: it.ChannelHandle,
			Domain:        it.Domain,
			Reason:        it.Reason,
			BlockedBy:     it.BlockedByUsername,
			BlockedAt:     it.BlockedAt,
		})
	}
	return c.JSON(http.StatusOK, blockedRemoteVideoListResponse{Videos: views, pageMeta: page.meta(total)})
}
