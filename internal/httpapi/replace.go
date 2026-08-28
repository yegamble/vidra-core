package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/upload"
	"github.com/vidra/vidra-core/internal/video"
)

// Video file replacement (config-parity W14, PT video_file.update): the owner
// (or a moderator/admin) uploads a NEW source for an already-published video.
// The video's id, URLs and metadata are stable across the replacement; playback
// keeps working mid-swap (the old HLS generation serves until the re-transcode
// promotes the new one — see video.ReplaceSource for the source-version model).
// Both upload shapes are supported: the direct multipart POST
// /videos/{id}/replace and the chunked replace-session (POST
// /videos/{id}/replace-session + the shared PUT-chunk/complete machinery; the
// session's purpose routes its completion here).

// videoReplaceEnabled is the runtime video_replace_enabled setting (default
// OFF — replacement is a new capability, not shipped behaviour).
func (s *Server) videoReplaceEnabled() bool {
	return s.settingBool(instancesettings.KeyVideoReplaceEnabled, false)
}

// videoReplaceAvailable is the EFFECTIVE availability: the runtime setting AND
// the instance-wide uploads gate (disabling uploads stops all content ingress,
// replacements included). Exposed as features.video_replace on GET /instance.
func (s *Server) videoReplaceAvailable() bool {
	return s.uploadsEnabled() && s.videoReplaceEnabled()
}

// replaceTarget authorises a replacement request: the video must exist and be
// visible to the caller (owner, or moderator/admin managing any local video —
// everyone else gets 404 so existence is not leaked), be published, and have
// no replacement already in flight (a live transcode job or an open replace
// session → 409 replace_conflict). viaSession marks a replace-session
// COMPLETION, which skips the open-session check — the completing session is
// itself the one holding that slot. Returns the video row and whether the
// actor manages a video they do not own.
func (s *Server) replaceTarget(ctx context.Context, c echo.Context, id uuid.UUID, viaSession bool) (sqlcgen.GetVideoByIDRow, bool, error) {
	userID, role, err := mustPrincipal(c)
	if err != nil {
		return sqlcgen.GetVideoByIDRow{}, false, err
	}
	// Staff (admin/moderator) replace any local video; an editor collaborator
	// (migration 0097) replaces their channel's videos. Both flow through the
	// ReplaceSource canManage escape.
	canManage := isStaff(role)
	v, err := s.videosvc.GetByID(ctx, id)
	if err != nil {
		return sqlcgen.GetVideoByIDRow{}, false, echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if v.OwnerID != userID && !canManage {
		canManage = s.canManageChannelContent(ctx, userID, v.ChannelID)
		if !canManage {
			return sqlcgen.GetVideoByIDRow{}, false, echo.NewHTTPError(http.StatusNotFound, "video not found")
		}
	}
	if v.State != "published" {
		return sqlcgen.GetVideoByIDRow{}, false, &ReplaceConflictError{Reason: "only a published video can have its file replaced"}
	}
	// A live transcode job means a transcode of the current (or a just-swapped)
	// source is still pending/running: enqueueing is idempotent per live job, so
	// a replacement accepted now would never be re-transcoded.
	if s.transcodesvc != nil && s.transcodesvc.HasLiveJob(ctx, id) {
		return sqlcgen.GetVideoByIDRow{}, false, &ReplaceConflictError{Reason: "the video is still being processed; try again once processing finishes"}
	}
	if !viaSession && s.uploadsvc != nil && s.uploadsvc.HasActiveReplaceSession(ctx, id) {
		return sqlcgen.GetVideoByIDRow{}, false, &ReplaceConflictError{Reason: "a replacement for this video is already in progress"}
	}
	return v, canManage, nil
}

// handleCreateReplaceSession opens a chunked/resumable upload session whose
// completion REPLACES the video's source (config-parity W14). Same validation
// order as the plain upload session — extension (415, honoring
// upload_additional_extensions_enabled), size cap (413), then the storage +
// rolling-daily quotas (422) — plus the replacement gates: the
// video_replace_enabled feature (403 feature_disabled) and the published/no
// -replacement-in-flight state (409 replace_conflict). Quotas are checked
// against the video's OWNER (storage usage aggregates against the owning
// account) even when a moderator/admin performs the replacement.
func (s *Server) handleCreateReplaceSession(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if !s.videoReplaceAvailable() {
		return &FeatureDisabledError{Feature: "video_replace"}
	}
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	var in createUploadSessionRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	v, _, terr := s.replaceTarget(ctx, c, id, false)
	if terr != nil {
		return terr
	}
	if _, ok := video.AcceptedVideoExtGated(in.Filename, s.uploadAdditionalExtensionsEnabled()); !ok {
		return echo.NewHTTPError(http.StatusUnsupportedMediaType, "unsupported media type")
	}
	if maxBytes := s.uploadMaxSizeBytes(); maxBytes > 0 && in.Size > maxBytes {
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "the file is too large")
	}
	if qerr := s.checkUploadQuotas(ctx, v.OwnerID, in.Size); qerr != nil {
		return qerr
	}
	sess, err := s.uploadsvc.CreateSession(ctx, id, userID, strings.TrimSpace(in.Filename), in.Size, strings.TrimSpace(in.FileFingerprint), upload.PurposeReplace)
	if err != nil {
		if errors.Is(err, upload.ErrTooManyActiveSessions) {
			return &TooManyActiveUploadsError{Max: s.uploadMaxActiveSessions()}
		}
		return err
	}
	total := int((sess.TotalSize + int64(sess.ChunkSize) - 1) / int64(sess.ChunkSize))
	return c.JSON(http.StatusCreated, uploadSessionResponse{
		UploadID:    sess.ID.String(),
		ChunkSize:   int(sess.ChunkSize),
		TotalChunks: total,
		Size:        sess.TotalSize,
		ExpiresAt:   sess.ExpiresAt,
	})
}

// authorizeReplaceCompletion decides, at request time, whether a replace-purpose
// upload session may be finalised — and returns the canManage bit the finalize
// worker carries into video.ReplaceSource.
//
// Everything here is a CURRENT-state re-check: the feature may have been turned
// off, quota headroom consumed, the video unpublished, or another replacement
// promoted while the chunks uploaded. It runs synchronously because
// authorisation belongs to the authenticated request; the worker has no
// principal and no role to re-derive it from, so the decision travels on the job
// row (upload_finalize_jobs.can_manage, migration 0120).
//
// The bytes themselves — assembly, scan, probe, the swap — are the worker's:
// video.ReplaceSource is exactly as expensive as the upload path's
// AttachOriginal → Process, and shares its route and therefore its deadline.
func (s *Server) authorizeReplaceCompletion(c echo.Context, sess sqlcgen.UploadSession) (bool, error) {
	ctx := c.Request().Context()
	if !s.videoReplaceAvailable() {
		return false, &FeatureDisabledError{Feature: "video_replace"}
	}
	v, canManage, terr := s.replaceTarget(ctx, c, sess.VideoID, true)
	if terr != nil {
		return false, terr
	}
	if qerr := s.checkUploadQuotas(ctx, v.OwnerID, sess.TotalSize); qerr != nil {
		return false, qerr
	}
	return canManage, nil
}

// handleReplaceVideoFile is the direct (multipart form field "file") shape of
// the replacement upload — the one-request counterpart of the replace session,
// mirroring POST /videos/{id}/file. Owner or moderator/admin; gates and quota
// semantics identical to the session shape. Responds 200 with the video + new
// source file.
func (s *Server) handleReplaceVideoFile(c echo.Context) error {
	if _, _, ok := principalFromContext(c); !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	if !s.videoReplaceAvailable() {
		return &FeatureDisabledError{Feature: "video_replace"}
	}
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	fh, err := c.FormFile("file")
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, `multipart form field "file" is required`)
	}
	ctx := c.Request().Context()
	v, canManage, terr := s.replaceTarget(ctx, c, id, false)
	if terr != nil {
		return terr
	}
	if maxBytes := s.uploadMaxSizeBytes(); maxBytes > 0 && fh.Size > maxBytes {
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "the file is too large")
	}
	if qerr := s.checkUploadQuotas(ctx, v.OwnerID, fh.Size); qerr != nil {
		return qerr
	}
	f, err := fh.Open()
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	userID, _, _ := principalFromContext(c)
	updated, file, rerr := s.videosvc.ReplaceSource(ctx, userID, id, video.UploadInput{
		Filename:    fh.Filename,
		ContentType: fh.Header.Get("Content-Type"),
		Reader:      f,
	}, canManage)
	if rerr != nil {
		return videoError(rerr)
	}
	s.orchestrateReplaceTranscode(ctx, id, file.StorageKey)
	return c.JSON(http.StatusOK, uploadVideoFileResponse{
		Video: newVideoView(updated),
		File:  newVideoFileView(file),
	})
}

// orchestrateReplaceTranscode routes a just-swapped source into the transcode
// pipeline: when the pipeline is effectively available (wired AND the runtime
// transcoding_enabled gate on) the re-transcode is enqueued — the old HLS
// generation keeps serving until transcode.storeResult promotes the new one.
// When it is NOT available, the stale playlist/rendition rows are invalidated
// instead, so HLS never keeps serving the superseded content over the new
// source (playback falls back to the progressive original). Best-effort like
// the publish-path enqueue: a failure is logged, never fails the replacement
// (the source swap already happened).
func (s *Server) orchestrateReplaceTranscode(ctx context.Context, videoID uuid.UUID, sourceKey string) {
	if s.transcodesvc == nil {
		return
	}
	if s.transcodesvc.Capable() && s.transcodesvc.Enabled() {
		if err := s.transcodesvc.Enqueue(ctx, videoID, sourceKey); err != nil {
			s.logger.Error("replace: transcode enqueue failed", "video_id", videoID.String(), "error", err)
		}
		return
	}
	if err := s.transcodesvc.Invalidate(ctx, videoID); err != nil {
		s.logger.Error("replace: playlist invalidation failed", "video_id", videoID.String(), "error", err)
	}
}
