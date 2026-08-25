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
	"github.com/vidra/vidra-core/internal/transcode"
	"github.com/vidra/vidra-core/internal/video"
)

type runVideoTranscodingRequest struct {
	Type string `json:"type"`
}

func (r runVideoTranscodingRequest) Validate() []FieldError {
	if r.Type != transcode.TargetHLS && r.Type != transcode.TargetWebVideo {
		return []FieldError{{Field: "type", Message: "must be hls or web_video"}}
	}
	return nil
}

// handleRunVideoTranscoding lets a moderator/admin rebuild one output class.
// The source key is resolved from video_files.kind='original' on the server;
// clients cannot submit a derivative key, so repeated runs never transcode an
// already-transcoded file and therefore cannot accumulate generation loss.
func (s *Server) handleRunVideoTranscoding(c echo.Context) error {
	actorID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	var in runVideoTranscodingRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if !s.transcodesvc.Enabled() {
		return &FeatureDisabledError{Feature: "transcoding"}
	}
	if !s.transcodesvc.Capable() {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "transcoding is not available on this server")
	}
	ctx := c.Request().Context()
	if _, err := s.videosvc.GetByID(ctx, id); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	sourceKey, err := s.videosvc.OriginalFileKey(ctx, id)
	if err != nil {
		return echo.NewHTTPError(http.StatusConflict, "video has no original file")
	}
	if s.transcodesvc.HasLiveJob(ctx, id) {
		return echo.NewHTTPError(http.StatusConflict, "a transcode job is already in progress for this video")
	}
	if err := s.transcodesvc.EnqueueTarget(ctx, id, sourceKey, in.Type); err != nil {
		return err
	}
	s.audit(c, observability.ActionVideoTranscode, observability.ResultSuccess,
		actorID.String(), "video="+id.String()+" type="+in.Type)
	return c.JSON(http.StatusAccepted, map[string]string{"status": "queued", "type": in.Type})
}

// adminVideoView is the admin/moderator videos-overview projection of a video.
type adminVideoView struct {
	ID                 string    `json:"id"`
	Title              string    `json:"title"`
	Privacy            string    `json:"privacy"`
	State              string    `json:"state"`
	ChannelHandle      string    `json:"channel_handle"`
	ChannelDisplayName string    `json:"channel_display_name"`
	Views              int64     `json:"views"`
	PublishedAt        time.Time `json:"published_at"`
	DurationSeconds    *int32    `json:"duration_seconds,omitempty"`
	IsLocal            bool      `json:"is_local"`
	OriginDomain       string    `json:"origin_domain,omitempty"`
	WatchURL           string    `json:"watch_url,omitempty"`
	Sensitive          bool      `json:"sensitive"`
	ExternalLink       bool      `json:"external_link"`
	HasThumbnail       bool      `json:"has_thumbnail"`
	HasOriginal        bool      `json:"has_original"`
	HLSCount           int32     `json:"hls_count"`
	WebVideoCount      int32     `json:"web_video_count"`
	// ObjectStorage is derived from the INSTANCE storage backend, not from
	// per-file truth: with STORAGE_BACKEND=s3 every local video reports true and
	// with local storage every local video reports false. That is why there is
	// deliberately no ?storage= filter on this endpoint — filtering on a value
	// that is constant across the whole result set would be meaningless. PeerTube
	// can offer that filter because it stores a per-file `storage` enum
	// (FileStorage.OBJECT_STORAGE); vidra has no per-file equivalent, and
	// recording that gap honestly is better than shipping a filter that lies.
	ObjectStorage bool  `json:"object_storage"`
	SizeBytes     int64 `json:"size_bytes"`
	Blocked       bool  `json:"blocked"`
	// Likes/Comments are the engagement counts the inventory sorts on. Federated
	// rows have no local ratings or comments and always report 0.
	Likes    int64 `json:"likes"`
	Comments int64 `json:"comments"`
}

// adminVideoListResponse is the paginated admin videos overview.
type adminVideoListResponse struct {
	Videos []adminVideoView `json:"videos"`
	pageMeta
}

// handleListAdminVideos returns the moderation inventory (local + federated,
// any privacy/state), each row with its current block status, plus `total` —
// how many videos match the SAME filters. Behind requireRole(admin, moderator).
//
// ?sort defaults to -created_at (the previous fixed ordering). ?q filters by
// title. ?state and ?privacy are repeatable and/or comma-separated. ?scope is
// local|remote|all. ?channel is an exact handle. ?published_after/?published_before
// are RFC3339. ?has_original/?has_hls/?has_web_files are tri-state: absent means
// "all", true/false narrow. Pagination via ?limit (1–100, default 20)/?offset.
//
// An unrecognised value for any of these is a 400 rather than a silent
// no-op — see parseEnumParam.
func (s *Server) handleListAdminVideos(c echo.Context) error {
	filter, err := parseAdminVideoFilter(c)
	if err != nil {
		return err
	}
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.videosvc.ListAdmin(c.Request().Context(), filter, page.Limit32(), page.Offset32())
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
			PublishedAt:        it.PublishedAt,
			DurationSeconds:    it.DurationSeconds,
			IsLocal:            it.IsLocal,
			OriginDomain:       it.OriginDomain,
			WatchURL:           it.WatchURL,
			Sensitive:          it.IsSensitive,
			ExternalLink:       it.ExternalLink,
			HasThumbnail:       it.HasThumbnail,
			HasOriginal:        it.HasOriginal,
			HLSCount:           it.HLSCount,
			WebVideoCount:      it.WebVideoCount,
			ObjectStorage:      it.IsLocal && s.cfg.StorageBackend == "s3",
			SizeBytes:          it.SizeBytes,
			Blocked:            it.Blocked,
			Likes:              it.Likes,
			Comments:           it.Comments,
		})
	}
	return c.JSON(http.StatusOK, adminVideoListResponse{Videos: views, pageMeta: page.meta(total)})
}

// parseAdminVideoFilter reads the inventory's sort + filter query params,
// rejecting anything outside the supported sets. The accepted values come from
// the video package (which derives them from the schema's own CHECK
// constraints), so there is no second hand-maintained copy to drift.
//
// There is deliberately no storage-location filter here; see the comment on
// adminVideoView.ObjectStorage.
func parseAdminVideoFilter(c echo.Context) (video.AdminFilter, error) {
	f := video.AdminFilter{
		Query:   strings.TrimSpace(c.QueryParam("q")),
		Channel: strings.TrimSpace(c.QueryParam("channel")),
	}
	var err error
	if f.Sort, err = parseSortParam(c, video.AdminSorts(), video.AdminSortDefault); err != nil {
		return f, err
	}
	if f.States, err = parseCSVEnumParam(c, "state", video.AdminStates()); err != nil {
		return f, err
	}
	if f.Privacies, err = parseCSVEnumParam(c, "privacy", video.AdminPrivacies()); err != nil {
		return f, err
	}
	if f.Scope, err = parseScopeParam(c); err != nil {
		return f, err
	}
	if f.PublishedAfter, f.PublishedBefore, err = parseTimeRangeParams(c, "published_after", "published_before"); err != nil {
		return f, err
	}
	if f.HasOriginal, err = parseBoolParam(c, "has_original"); err != nil {
		return f, err
	}
	if f.HasHLS, err = parseBoolParam(c, "has_hls"); err != nil {
		return f, err
	}
	if f.HasWebFiles, err = parseBoolParam(c, "has_web_files"); err != nil {
		return f, err
	}
	if len(f.Channel) > 255 {
		return f, echo.NewHTTPError(http.StatusBadRequest, "channel must be at most 255 characters")
	}
	return f, nil
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
	pageMeta
}

// handleListQuarantinedVideos returns quarantined uploads newest first for the
// moderation review queue (§11). Behind requireRole(admin, moderator).
// Pagination via ?limit (1–100, default 20) and ?offset.
func (s *Server) handleListQuarantinedVideos(c echo.Context) error {
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.videosvc.ListQuarantined(c.Request().Context(), page.Limit32(), page.Offset32())
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
	return c.JSON(http.StatusOK, quarantinedVideoListResponse{Videos: views, pageMeta: page.meta(total)})
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
