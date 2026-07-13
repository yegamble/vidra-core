package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/searchevents"
	"github.com/vidra/vidra-core/internal/video"
)

// adminVideoView is the admin/moderator videos-overview projection of a video.
type adminVideoView struct {
	ID                 string    `json:"id"`
	Title              string    `json:"title"`
	Privacy            string    `json:"privacy"`
	State              string    `json:"state"`
	ChannelHandle      string    `json:"channel_handle"`
	ChannelDisplayName string    `json:"channel_display_name"`
	Views              int64     `json:"views"`
	CreatedAt          time.Time `json:"created_at"`
	Blocked            bool      `json:"blocked"`
}

// adminVideoListResponse is the paginated admin videos overview.
type adminVideoListResponse struct {
	Videos []adminVideoView `json:"videos"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

// handleListAdminVideos returns all videos (any privacy/state) newest first for
// moderators/admins, each with its current block status. Behind
// requireRole(admin, moderator). Optional ?q filters by title; pagination via
// ?limit (1–100, default 20) and ?offset.
func (s *Server) handleListAdminVideos(c echo.Context) error {
	q := c.QueryParam("q")
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	items, err := s.videosvc.ListAdmin(c.Request().Context(), q, int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]adminVideoView, 0, len(items))
	for _, it := range items {
		views = append(views, adminVideoView{
			ID:                 it.ID.String(),
			Title:              it.Title,
			Privacy:            it.Privacy,
			State:              it.State,
			ChannelHandle:      it.ChannelHandle,
			ChannelDisplayName: it.ChannelDisplayName,
			Views:              it.Views,
			CreatedAt:          it.CreatedAt,
			Blocked:            it.Blocked,
		})
	}
	return c.JSON(http.StatusOK, adminVideoListResponse{Videos: views, Limit: limit, Offset: offset})
}

// quarantinedVideoView is the moderation quarantine-queue projection of a held
// video.
type quarantinedVideoView struct {
	ID                 string    `json:"id"`
	Title              string    `json:"title"`
	Privacy            string    `json:"privacy"`
	State              string    `json:"state"`
	ChannelHandle      string    `json:"channel_handle"`
	ChannelDisplayName string    `json:"channel_display_name"`
	OwnerUsername      string    `json:"owner_username"`
	CreatedAt          time.Time `json:"created_at"`
}

// quarantinedVideoListResponse is the paginated quarantine queue.
type quarantinedVideoListResponse struct {
	Videos []quarantinedVideoView `json:"videos"`
	Limit  int                    `json:"limit"`
	Offset int                    `json:"offset"`
}

// handleListQuarantinedVideos returns quarantined uploads newest first for the
// moderation review queue (§11). Behind requireRole(admin, moderator).
// Pagination via ?limit (1–100, default 20) and ?offset.
func (s *Server) handleListQuarantinedVideos(c echo.Context) error {
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	items, err := s.videosvc.ListQuarantined(c.Request().Context(), int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]quarantinedVideoView, 0, len(items))
	for _, it := range items {
		views = append(views, quarantinedVideoView{
			ID:                 it.ID.String(),
			Title:              it.Title,
			Privacy:            it.Privacy,
			State:              it.State,
			ChannelHandle:      it.ChannelHandle,
			ChannelDisplayName: it.ChannelDisplayName,
			OwnerUsername:      it.OwnerUsername,
			CreatedAt:          it.CreatedAt,
		})
	}
	return c.JSON(http.StatusOK, quarantinedVideoListResponse{Videos: views, Limit: limit, Offset: offset})
}

// handleApproveQuarantinedVideo releases a quarantined video: it publishes
// through the real publish transition, so the federation-announce and
// transcode-enqueue hooks fire at approval time. Behind requireRole(admin,
// moderator). Unknown id → 404; a video not in quarantine → 409. Emits an
// audit event.
func (s *Server) handleApproveQuarantinedVideo(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if _, err := s.videosvc.ApproveQuarantined(c.Request().Context(), id); err != nil {
		switch {
		case errors.Is(err, video.ErrNotFound):
			s.audit(c, observability.ActionVideoApprove, observability.ResultFailure, userID.String(), "not_found")
			return echo.NewHTTPError(http.StatusNotFound, "video not found")
		case errors.Is(err, video.ErrNotQuarantined):
			s.audit(c, observability.ActionVideoApprove, observability.ResultFailure, userID.String(), "not_quarantined video="+id.String())
			return echo.NewHTTPError(http.StatusConflict, "video is not quarantined")
		}
		return err
	}
	s.audit(c, observability.ActionVideoApprove, observability.ResultSuccess, userID.String(), "video="+id.String())
	return c.NoContent(http.StatusNoContent)
}

// rejectQuarantinedVideoRequest is the optional POST /admin/videos/{id}/reject
// body; the reason is recorded in the audit trail (it may be empty).
type rejectQuarantinedVideoRequest struct {
	Reason string `json:"reason"`
}

func (r rejectQuarantinedVideoRequest) Validate() []FieldError {
	if len(r.Reason) > maxReportReasonLen {
		return []FieldError{{Field: "reason", Message: "must be at most 2000 characters"}}
	}
	return nil
}

// handleRejectQuarantinedVideo fails a quarantined video (it never publishes)
// and notifies the owner (best-effort; the moderator's identity is not
// exposed). Behind requireRole(admin, moderator). Unknown id → 404; a video not
// in quarantine → 409. Emits an audit event with a stable rejection
// classification; moderator prose remains outside the security ledger.
func (s *Server) handleRejectQuarantinedVideo(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	var in rejectQuarantinedVideoRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	v, err := s.videosvc.RejectQuarantined(ctx, id)
	if err != nil {
		switch {
		case errors.Is(err, video.ErrNotFound):
			s.audit(c, observability.ActionVideoReject, observability.ResultFailure, userID.String(), "not_found")
			return echo.NewHTTPError(http.StatusNotFound, "video not found")
		case errors.Is(err, video.ErrNotQuarantined):
			s.audit(c, observability.ActionVideoReject, observability.ResultFailure, userID.String(), "not_quarantined video="+id.String())
			return echo.NewHTTPError(http.StatusConflict, "video is not quarantined")
		}
		return err
	}
	if s.notifsvc != nil {
		if nerr := s.notifsvc.NotifyVideoRejected(ctx, v.OwnerID, userID, id); nerr != nil {
			s.logger.WarnContext(ctx, "notify video rejection failed", "error", nerr, "video_id", id)
		}
	}
	// Search: a rejected quarantined video must never surface (search-service
	// W4). Best-effort suppression (it was likely never indexed, but this is the
	// safe backstop).
	s.searchEvents.EnqueueVideoSuppress(ctx, id, searchevents.SuppressModerated)
	// The moderator's prose remains in the moderation workflow/notification; the
	// security ledger stores only a stable classification and whether prose was
	// supplied. Free-form content can contain PII and must never enter audit_log.
	s.auditEvent(c, audit.Event{
		Action: observability.ActionVideoReject, Result: observability.ResultSuccess,
		ActorID: userID.String(), Reason: "moderator_rejected",
		ResourceType: "video", ResourceID: id.String(),
		Metadata: []audit.MetadataField{{
			Key: "reason_provided", Value: strconv.FormatBool(strings.TrimSpace(in.Reason) != ""),
		}},
	})
	return c.NoContent(http.StatusNoContent)
}
