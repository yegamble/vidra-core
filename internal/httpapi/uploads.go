package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/upload"
	"github.com/vidra/vidra-core/internal/video"
)

// createUploadSessionRequest is the POST /videos/:id/upload-session body: the
// total file size and the client filename (its extension is validated up front
// against the same video-container allow-list a direct upload enforces).
type createUploadSessionRequest struct {
	Size     int64  `json:"size"`
	Filename string `json:"filename"`
	// FileFingerprint is an OPAQUE client identity for the file (recommended
	// recipe: SHA-256 over size + first/last 1 MiB) used for cross-refresh /
	// cross-device resume (UPLOAD-03). Optional; <=128 chars; stored verbatim,
	// never parsed.
	FileFingerprint string `json:"file_fingerprint"`
}

// maxFileFingerprintLen bounds the opaque resume fingerprint (a SHA-256 hex
// digest is 64 chars; 128 leaves headroom for other client recipes).
const maxFileFingerprintLen = 128

func (r createUploadSessionRequest) Validate() []FieldError {
	var fes []FieldError
	if strings.TrimSpace(r.Filename) == "" {
		fes = append(fes, FieldError{Field: "filename", Message: "is required"})
	}
	if r.Size <= 0 {
		fes = append(fes, FieldError{Field: "size", Message: "must be greater than zero"})
	}
	if len(r.FileFingerprint) > maxFileFingerprintLen {
		fes = append(fes, FieldError{Field: "file_fingerprint", Message: "must be at most 128 characters"})
	}
	return fes
}

// uploadSessionResponse is returned when a resumable upload is opened.
type uploadSessionResponse struct {
	UploadID    string    `json:"upload_id"`
	ChunkSize   int       `json:"chunk_size"`
	TotalChunks int       `json:"total_chunks"`
	Size        int64     `json:"size"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// uploadStatusResponse is the resume/progress contract (GET /uploads/:id, the
// PUT-chunk ack, and the 202 a completion answers with): which chunk indices
// have landed, the byte total so far, and — since completion became
// asynchronous — where the session is in its lifecycle.
//
// State is the whole client contract for the post-100% phase:
//
//	active     → the client is still sending chunks
//	queued     → completion accepted, a finalize job is enqueued
//	processing → a worker is assembling/storing/probing the file
//	completed  → the pipeline finished; read the VIDEO for its outcome (a probe
//	             or scan may still have failed it, exactly as before)
//	failed     → the finalize dead-lettered; failure_reason says why
//	cancelled  → the client cancelled before completing
type uploadStatusResponse struct {
	UploadID       string    `json:"upload_id"`
	VideoID        string    `json:"video_id"`
	State          string    `json:"state"`
	Size           int64     `json:"size"`
	ChunkSize      int       `json:"chunk_size"`
	TotalChunks    int       `json:"total_chunks"`
	ReceivedChunks []int     `json:"received_chunks"`
	BytesReceived  int64     `json:"bytes_received"`
	ExpiresAt      time.Time `json:"expires_at"`
	// FailureReason is a safe, human-readable sentence explaining a 'failed'
	// session, and empty for every other state. It is what a polling client
	// shows instead of a generic error.
	FailureReason string `json:"failure_reason"`
}

func uploadStatusView(st upload.Status) uploadStatusResponse {
	received := st.ReceivedChunks
	if received == nil {
		received = []int{}
	}
	return uploadStatusResponse{
		UploadID:       st.Session.ID.String(),
		VideoID:        st.Session.VideoID.String(),
		State:          st.Session.State,
		Size:           st.Session.TotalSize,
		ChunkSize:      int(st.Session.ChunkSize),
		TotalChunks:    st.TotalChunks,
		ReceivedChunks: received,
		BytesReceived:  st.BytesReceived,
		ExpiresAt:      st.Session.ExpiresAt,
		FailureReason:  st.Session.FailureReason,
	}
}

// activeUploadResponse is one entry in GET /api/v1/me/uploads — a resumable
// session still open for the caller, with enough to reconstruct/continue it
// after a refresh or on another device (UPLOAD-03).
type activeUploadResponse struct {
	UploadID        string    `json:"upload_id"`
	VideoID         string    `json:"video_id"`
	Filename        string    `json:"filename"`
	Size            int64     `json:"size"`
	ChunkSize       int       `json:"chunk_size"`
	TotalChunks     int       `json:"total_chunks"`
	ReceivedChunks  int       `json:"received_chunks"`
	FileFingerprint string    `json:"file_fingerprint"`
	ExpiresAt       time.Time `json:"expires_at"`
}

// activeUploadsResponse wraps the caller's active resumable sessions.
type activeUploadsResponse struct {
	Uploads []activeUploadResponse `json:"uploads"`
}

// handleListMyUploads lists the caller's ACTIVE resumable upload sessions with
// each session's received-chunk count — the server-side resume contract
// (UPLOAD-03) that lets a client recover in-progress uploads after losing its
// localStorage (a refresh, or a different device). The optional ?fingerprint=
// query narrows the list to sessions whose file_fingerprint matches, so a client
// can ask "am I already uploading this exact file?". Auth required; only the
// caller's own sessions are ever returned.
func (s *Server) handleListMyUploads(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	fingerprint := strings.TrimSpace(c.QueryParam("fingerprint"))
	ups, err := s.uploadsvc.ActiveSessionsForUser(c.Request().Context(), userID, fingerprint)
	if err != nil {
		return err
	}
	out := make([]activeUploadResponse, 0, len(ups))
	for _, u := range ups {
		out = append(out, activeUploadResponse{
			UploadID:        u.ID.String(),
			VideoID:         u.VideoID.String(),
			Filename:        u.Filename,
			Size:            u.TotalSize,
			ChunkSize:       int(u.ChunkSize),
			TotalChunks:     u.TotalChunks,
			ReceivedChunks:  u.ReceivedChunks,
			FileFingerprint: u.FileFingerprint,
			ExpiresAt:       u.ExpiresAt,
		})
	}
	return c.JSON(http.StatusOK, activeUploadsResponse{Uploads: out})
}

// handleCreateUploadSession opens a chunked/resumable upload for a video the
// caller owns (owner only; non-owner/unknown → 404). The size, filename
// extension, and storage quota are validated up front: a non-video extension is
// 415, a size beyond UPLOAD_MAX_SIZE is 413, and a size that would exceed the
// caller's storage quota is 422 quota_exceeded. Returns the upload id, the fixed
// chunk size to send, and the 24h expiry.
func (s *Server) handleCreateUploadSession(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if !s.uploadsEnabled() {
		return &FeatureDisabledError{Feature: "uploads"}
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
	// Owner OR editor collaborator (migration 0097). The session is owned by the
	// caller; quotas count against the channel owner (v.OwnerID), consistent with
	// where the stored bytes land on complete.
	v, canManage := s.canManageVideo(ctx, userID, id)
	if !canManage {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	// Extension allow-list up front (same gate AttachOriginal enforces on
	// complete), honoring the runtime additional-extensions setting
	// (upload_additional_extensions_enabled, config-parity W10).
	if _, ok := video.AcceptedVideoExtGated(in.Filename, s.uploadAdditionalExtensionsEnabled()); !ok {
		return echo.NewHTTPError(http.StatusUnsupportedMediaType, "unsupported media type")
	}
	// Size vs the effective upload cap up front (413). The cap is the DB overlay
	// (default_… from UPLOAD_MAX_SIZE) so an admin can retune it without a restart.
	if maxBytes := s.uploadMaxSizeBytes(); maxBytes > 0 && in.Size > maxBytes {
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "the file is too large")
	}
	// Quotas up front: total (422 quota_exceeded) then the rolling daily
	// window (422 daily_quota_exceeded, config-parity W7).
	if qerr := s.checkUploadQuotas(ctx, v.OwnerID, in.Size); qerr != nil {
		return qerr
	}
	sess, err := s.uploadsvc.CreateSession(ctx, id, userID, strings.TrimSpace(in.Filename), in.Size, strings.TrimSpace(in.FileFingerprint), upload.PurposeUpload)
	if err != nil {
		// Batch guard (UPLOAD-10): the caller holds the max concurrent active
		// sessions → 429 too_many_active_uploads so the client queues on it.
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

// handlePutUploadChunk stores one chunk (raw request body) at index n for a
// session the caller owns. Idempotent re-PUT. Returns the updated received-chunk
// status. Non-owner/unknown session → 404; a session that already
// completed/cancelled → 409; an out-of-range index or wrong-sized chunk → 422; a
// chunk larger than its slot → 413.
func (s *Server) handlePutUploadChunk(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	uploadID, err := pathUUID(c, "upload_id", "upload session not found")
	if err != nil {
		return err
	}
	n, err := strconv.Atoi(c.Param("n"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid chunk index")
	}
	st, err := s.uploadsvc.PutChunk(c.Request().Context(), uploadID, userID, n, c.Request().Body)
	if err != nil {
		return uploadError(err)
	}
	return c.JSON(http.StatusOK, uploadStatusView(st))
}

// handleGetUploadSession returns a session's received-chunk status — the resume
// contract a client reads to know which chunks to (re)send. Owner only;
// non-owner/unknown → 404.
func (s *Server) handleGetUploadSession(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	uploadID, err := pathUUID(c, "upload_id", "upload session not found")
	if err != nil {
		return err
	}
	st, err := s.uploadsvc.StatusFor(c.Request().Context(), uploadID, userID)
	if err != nil {
		return uploadError(err)
	}
	return c.JSON(http.StatusOK, uploadStatusView(st))
}

// handleCompleteUploadSession ACCEPTS a completion: it validates cheaply,
// enqueues the finalize job, and answers 202 with the session's state. The
// client then polls GET /api/v1/uploads/{upload_id} until the session is
// completed (read the video for its outcome) or failed (failure_reason says
// why).
//
// It used to do the work inline — assemble every chunk back out of object
// storage, re-upload the assembled file while hashing it, ffprobe it, and decode
// it twice more for the thumbnail and storyboard — all under the general 30s
// request deadline, and (in the shipped deployment) behind a CDN that caps
// origin response time at around 100s. On a remote bucket that is minutes of
// work, so every real upload 5xx'd the moment the progress bar hit 100%, and no
// deadline change could have made a synchronous version reliable behind that
// CDN. See internal/uploadfinalize.
//
// Owner only; non-owner/unknown → 404; cancelled → 409; missing/mismatched
// chunks → 422; over quota → 422. Re-POSTing is idempotent: while the finalize
// is queued/processing it answers 202 with the current state without queueing a
// second run, and on a session that already finished it answers 200 with the
// terminal state.
func (s *Server) handleCompleteUploadSession(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	uploadID, err := pathUUID(c, "upload_id", "upload session not found")
	if err != nil {
		return err
	}
	if s.uploadfinalizesvc == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "upload completion is unavailable")
	}
	ctx := c.Request().Context()

	// Idempotency first: a session that already left 'active' is answered from
	// its current state rather than re-validated and re-enqueued. A client
	// retrying a completion whose response it never saw must not queue the
	// pipeline twice, and must never be told "409" for work it did successfully.
	st, err := s.uploadsvc.StatusFor(ctx, uploadID, userID)
	if err != nil {
		return uploadError(err)
	}
	switch st.Session.State {
	case upload.StateQueued, upload.StateProcessing:
		return c.JSON(http.StatusAccepted, uploadStatusView(st))
	case upload.StateCompleted, upload.StateFailed:
		return c.JSON(http.StatusOK, uploadStatusView(st))
	case upload.StateCancelled:
		return uploadError(upload.ErrNotActive)
	}

	sess, err := s.uploadsvc.ValidateComplete(ctx, uploadID, userID)
	if err != nil {
		return uploadError(err)
	}

	// A replace-purpose session (config-parity W14) finalises through the
	// source-replacement flow. Its gates — the video_replace feature, the
	// published/no-replacement-in-flight state — are decided HERE, by the
	// authenticated request, and the resulting authorisation travels on the job:
	// a worker has no principal to re-derive it from.
	canManage := false
	if sess.Purpose == upload.PurposeReplace {
		if canManage, err = s.authorizeReplaceCompletion(c, sess); err != nil {
			return err
		}
	} else {
		// The session belongs to the caller (ValidateComplete enforced that);
		// confirm they still manage the target video's channel and resolve the
		// channel owner so an editor collaborator's upload (migration 0097)
		// lands under, and counts against, the owner.
		mv, ok := s.canManageVideo(ctx, userID, sess.VideoID)
		if !ok {
			return echo.NewHTTPError(http.StatusNotFound, "video not found")
		}
		// Re-check the quotas (total + rolling daily) against the assembled size
		// before accepting (the session may have sat while other uploads
		// consumed headroom).
		if qerr := s.checkUploadQuotas(ctx, mv.OwnerID, sess.TotalSize); qerr != nil {
			return qerr
		}
	}

	if _, err := s.uploadfinalizesvc.Enqueue(ctx, uploadID, sess.VideoID, sess.Purpose, canManage); err != nil {
		return err
	}
	// Re-read so the 202 carries the session's NEW state ('queued') rather than
	// the 'active' snapshot taken above.
	queued, err := s.uploadsvc.StatusFor(ctx, uploadID, userID)
	if err != nil {
		return uploadError(err)
	}
	return c.JSON(http.StatusAccepted, uploadStatusView(queued))
}

// handleCancelUploadSession cancels an in-progress upload and drops its chunk
// blobs. Owner only; non-owner/unknown → 404. Idempotent (cancelling an
// already-finished session still 204s).
func (s *Server) handleCancelUploadSession(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	uploadID, err := pathUUID(c, "upload_id", "upload session not found")
	if err != nil {
		return err
	}
	if err := s.uploadsvc.Cancel(c.Request().Context(), uploadID, userID); err != nil {
		return uploadError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// uploadError maps upload service sentinels to HTTP error envelopes.
func uploadError(err error) error {
	switch {
	case errors.Is(err, upload.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "upload session not found")
	case errors.Is(err, upload.ErrNotActive):
		return echo.NewHTTPError(http.StatusConflict, "upload session is no longer active")
	case errors.Is(err, upload.ErrChunkRange):
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "chunk index out of range")
	case errors.Is(err, upload.ErrChunkSize):
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "chunk size does not match the expected size for its index")
	case errors.Is(err, upload.ErrChunkTooLarge):
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "the chunk is too large")
	case errors.Is(err, upload.ErrIncomplete):
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "the upload is missing one or more chunks")
	default:
		return err
	}
}
