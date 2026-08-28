// Package uploadfinalize runs the expensive half of a resumable upload's
// completion on a durable queue instead of inside the request.
//
// # Why it exists
//
// POST /api/v1/uploads/{upload_id}/complete used to do all of this
// synchronously: stream every 8 MiB chunk back down from object storage,
// re-upload the assembled file while hashing it (video.AttachOriginal →
// storage.PutSizedHashed), then ffprobe it and decode it twice more for the
// thumbnail and the storyboard (video.Process). Only the transcode was already
// asynchronous.
//
// That is minutes of work for a real video when the object store is Backblaze B2
// across the internet, and the route was not classified as streaming — so it
// carried the general 30s request/socket deadline. Worse, the deployment sits
// behind a CDN that caps how long an origin may take to respond (~100s), so
// raising the deadline could not have fixed it either: no synchronous version of
// this endpoint can be reliable there. What a creator saw was the progress bar
// reaching 100% and then a 5xx.
//
// So completion is now an ENQUEUE. The request keeps the cheap validation
// (upload.ValidateComplete: ownership, session state, every chunk present at its
// required size) plus the quota re-check, and answers 202. This queue carries
// the rest.
//
// # Shape
//
// Deliberately a copy of internal/videoimport's queue wiring, because the
// sweep (internal/jobrecovery), the operational projection (migration 0083/0120)
// and the operator's mental model all already understand that shape:
// pending → running → done | failed, claimed with FOR UPDATE SKIP LOCKED,
// lease renewed while the worker runs, exponential backoff on retry, dead-letter
// after maxAttempts. On dead-letter the SESSION is marked failed with a safe
// reason, which is what the client's poll surfaces.
//
// It is HTTP-agnostic and testable with fakes for every collaborator.
package uploadfinalize

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/lease"
	"github.com/vidra/vidra-core/internal/retry"
	"github.com/vidra/vidra-core/internal/safeerr"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/upload"
	"github.com/vidra/vidra-core/internal/video"
	"github.com/vidra/vidra-core/internal/workerpool"
)

const (
	// maxAttempts is how many times a finalize is tried before dead-lettering.
	maxAttempts = 5
	// baseBackoff is the first retry delay; it doubles each attempt.
	baseBackoff = time.Minute
	// maxBackoff caps the exponential backoff.
	maxBackoff = time.Hour
	// maxErrorLen bounds the stored (client-visible) error string.
	maxErrorLen = 300
)

// Job states persisted on upload_finalize_jobs.
const (
	StatePending = "pending"
	StateRunning = "running"
	StateDone    = "done"
	StateFailed  = "failed"
)

// ErrNotFound means the session has no finalize job.
var ErrNotFound = errors.New("uploadfinalize: no finalize job")

// Repository is the data access the finalize service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	EnqueueUploadFinalizeJob(ctx context.Context, arg sqlcgen.EnqueueUploadFinalizeJobParams) (sqlcgen.UploadFinalizeJob, error)
	GetLatestUploadFinalizeJob(ctx context.Context, uploadID uuid.UUID) (sqlcgen.UploadFinalizeJob, error)
	DeleteUploadFinalizeJob(ctx context.Context, id uuid.UUID) error
	ClaimDueUploadFinalizeJobs(ctx context.Context, limit int32) ([]sqlcgen.ClaimDueUploadFinalizeJobsRow, error)
	RenewUploadFinalizeJobLease(ctx context.Context, id uuid.UUID) error
	CompleteUploadFinalizeJob(ctx context.Context, id uuid.UUID) error
	RescheduleUploadFinalizeJob(ctx context.Context, arg sqlcgen.RescheduleUploadFinalizeJobParams) error
	FailUploadFinalizeJob(ctx context.Context, arg sqlcgen.FailUploadFinalizeJobParams) error
}

// Pipeline is the video ingest seam: store the assembled bytes as the video's
// original and finalise through the shared publish pipeline, or — for a
// replace-purpose session — swap the source of a published video.
// *video.Service satisfies it; a fake in tests.
type Pipeline interface {
	GetByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error)
	AttachOriginal(ctx context.Context, ownerID, videoID uuid.UUID, in video.UploadInput) (sqlcgen.Video, sqlcgen.VideoFile, error)
	Process(ctx context.Context, videoID uuid.UUID, originalKey string) (sqlcgen.Video, error)
	ReplaceSource(ctx context.Context, actorID, videoID uuid.UUID, in video.UploadInput, canManage bool) (sqlcgen.Video, sqlcgen.VideoFile, error)
}

// Service runs the asynchronous completion queue.
type Service struct {
	repo     Repository
	uploads  *upload.Service
	pipeline Pipeline
	logger   *slog.Logger
	// onReplaced routes a just-swapped source into the transcode pipeline (the
	// replace-purpose equivalent of the onTranscode hook Process fires for a
	// plain upload). Best-effort: the swap already happened.
	onReplaced func(ctx context.Context, videoID uuid.UUID, sourceKey string)
	// concurrencyFn is the runtime worker parallelism, resolved per DrainJobs
	// call so a change applies without a restart. nil = 1.
	concurrencyFn func() int64
}

// Option customises the Service.
type Option func(*Service)

// WithLogger overrides the logger used for unexpected (non-client-facing) errors.
func WithLogger(l *slog.Logger) Option {
	return func(s *Service) {
		if l != nil {
			s.logger = l
		}
	}
}

// WithReplaceHook wires what happens after a replace-purpose session swaps a
// video's source: enqueue the re-transcode when the pipeline is available, or
// invalidate the stale playlist when it is not (see the HTTP layer's
// orchestrateReplaceTranscode, which is the same decision for the direct
// multipart shape). A plain upload needs no hook — video.Process fires its own
// transcode enqueue.
func WithReplaceHook(fn func(ctx context.Context, videoID uuid.UUID, sourceKey string)) Option {
	return func(s *Service) { s.onReplaced = fn }
}

// WithConcurrencyFunc makes the worker parallelism dynamic; f is resolved once
// per DrainJobs call and clamped to [1, workerpool.MaxConcurrency].
func WithConcurrencyFunc(f func() int64) Option {
	return func(s *Service) { s.concurrencyFn = f }
}

// Concurrency is the effective parallelism for the next drain.
func (s *Service) Concurrency() int {
	if s.concurrencyFn == nil {
		return 1
	}
	return workerpool.Clamp(s.concurrencyFn())
}

// NewService builds the finalize service.
func NewService(repo Repository, uploads *upload.Service, pipeline Pipeline, opts ...Option) *Service {
	s := &Service{repo: repo, uploads: uploads, pipeline: pipeline, logger: slog.Default()}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Enqueue queues the completion of an upload session and flips the session to
// 'queued'. The caller has already run upload.ValidateComplete (ownership,
// active state, every chunk landed) and the quota re-check.
//
// Idempotent per session: while a pending/running job exists the insert is a
// no-op (partial unique index) and the in-flight job is returned unchanged, so a
// client retrying a completion whose response it never saw does not queue the
// pipeline twice.
//
// canManage carries the request-time authorisation for a replace-purpose
// session performed by staff or an editor collaborator on a video they do not
// own; it is meaningless (and false) for a plain upload.
func (s *Service) Enqueue(ctx context.Context, uploadID, videoID uuid.UUID, purpose string, canManage bool) (sqlcgen.UploadFinalizeJob, error) {
	if purpose != upload.PurposeReplace {
		purpose = upload.PurposeUpload
	}
	job, err := s.repo.EnqueueUploadFinalizeJob(ctx, sqlcgen.EnqueueUploadFinalizeJobParams{
		UploadID:  uploadID,
		VideoID:   videoID,
		Purpose:   purpose,
		CanManage: canManage,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// A finalize is already in flight for this session — return it unchanged
		// and leave the session state alone.
		return s.repo.GetLatestUploadFinalizeJob(ctx, uploadID)
	}
	if err != nil {
		return sqlcgen.UploadFinalizeJob{}, err
	}
	// The session moves to 'queued' only once a job actually landed: a session
	// advertising 'queued' with nothing queued is the one state a poller can
	// never escape.
	//
	// The transition is a CAS on state = 'active', and it can legitimately lose:
	// a DELETE /uploads/{id} may have landed between the caller's
	// ValidateComplete and here, cancelling the session and deleting its chunk
	// blobs. Writing 'queued' regardless would undo the user's cancel and leave
	// this job to spend its whole retry budget failing on bytes that are gone —
	// so instead the job is removed and the caller answers 409.
	queued, err := s.uploads.MarkQueued(ctx, uploadID)
	if err != nil {
		return sqlcgen.UploadFinalizeJob{}, err
	}
	if !queued {
		if derr := s.repo.DeleteUploadFinalizeJob(ctx, job.ID); derr != nil {
			// The job survives as 'pending' against a non-active session. It is
			// bounded — Assemble refuses anything but queued/processing, so the
			// worker dead-letters it — but it holds the partial unique index and
			// keeps the sweeper off the session until it does, which is worth a
			// line in the log.
			s.logger.Error("upload finalize: could not drop the job for a session cancelled mid-enqueue",
				"job_id", job.ID.String(), "upload_id", uploadID.String(), "error", derr.Error())
		}
		return sqlcgen.UploadFinalizeJob{}, upload.ErrNotActive
	}
	return job, nil
}

// LatestForSession returns a session's most recent finalize job. ErrNotFound
// when completion was never requested.
func (s *Service) LatestForSession(ctx context.Context, uploadID uuid.UUID) (sqlcgen.UploadFinalizeJob, error) {
	job, err := s.repo.GetLatestUploadFinalizeJob(ctx, uploadID)
	if err != nil {
		return sqlcgen.UploadFinalizeJob{}, ErrNotFound
	}
	return job, nil
}

// DrainJobs claims up to limit due jobs and runs each through assembly + the
// pipeline. On success the job completes and the session is marked completed; on
// failure it is rescheduled with backoff, or dead-lettered (and the session
// marked failed with the safe reason) after maxAttempts. Returns the number
// completed. Only the claim-query error is returned — per-job outcomes are
// persisted in the queue, not surfaced.
//
// Intended to be called on a ticker by a single worker goroutine per instance.
func (s *Service) DrainJobs(ctx context.Context, limit int) (int, error) {
	rows, err := s.repo.ClaimDueUploadFinalizeJobs(ctx, int32(limit))
	if err != nil {
		return 0, err
	}
	var (
		mu   sync.Mutex
		done int
	)
	workerpool.Run(s.Concurrency(), len(rows), func(i int) {
		row := rows[i]
		// Assembling a multi-gigabyte upload out of object storage and probing
		// it routinely outlives one lease, so renew while the work runs —
		// otherwise the sweep hands the same session to a second worker, and
		// two AttachOriginal calls race on the same deterministic key.
		stopLease := lease.Keep(ctx, lease.DefaultInterval, "upload_finalize_job", func(c context.Context) error {
			return s.repo.RenewUploadFinalizeJobLease(c, row.ID)
		})
		err := s.runFinalize(ctx, row)
		stopLease()
		if err != nil {
			s.recordFailure(ctx, row, err)
			return
		}
		if cerr := s.repo.CompleteUploadFinalizeJob(ctx, row.ID); cerr != nil {
			s.logger.Error("upload finalize: job finished but could not be marked done; the row stays 'running' until the lease sweep",
				"job_id", row.ID.String(), "upload_id", row.UploadID.String(), "error", cerr.Error())
		}
		if merr := s.uploads.MarkCompleted(ctx, row.UploadID); merr != nil {
			s.logger.Error("upload finalize: session could not be marked completed; the client's poll will not settle until the sweep",
				"upload_id", row.UploadID.String(), "error", merr.Error())
		}
		mu.Lock()
		done++
		mu.Unlock()
	})
	return done, nil
}

// runFinalize performs one completion: assemble the chunks into an ordered
// stream and run it through the pipeline the session's purpose selects. Every
// returned error carries a SAFE, client-visible message; unexpected internal
// errors are logged and reported generically.
func (s *Service) runFinalize(ctx context.Context, row sqlcgen.ClaimDueUploadFinalizeJobsRow) error {
	// Best-effort progress for the client's poll — a bookkeeping failure must
	// not fail a job whose real work is about to succeed.
	if err := s.uploads.MarkProcessing(ctx, row.UploadID); err != nil {
		s.logger.Warn("upload finalize: could not mark the session processing", "upload_id", row.UploadID.String(), "error", err)
	}

	sess, reader, err := s.uploads.Assemble(ctx, row.UploadID)
	if err != nil {
		switch {
		case errors.Is(err, upload.ErrIncomplete):
			return safeerr.New("some of the uploaded parts are missing; upload the file again")
		case errors.Is(err, upload.ErrNotFound), errors.Is(err, upload.ErrNotActive):
			return safeerr.New("the upload session is no longer available")
		default:
			return s.internalf("assemble chunks", err)
		}
	}
	defer func() { _ = reader.Close() }()

	v, err := s.pipeline.GetByID(ctx, row.VideoID)
	if err != nil {
		return safeerr.New("the video no longer exists")
	}

	if row.Purpose == upload.PurposeReplace {
		return s.runReplace(ctx, row, sess, reader)
	}

	// The bytes land under, and count against, the video's channel OWNER — the
	// same attribution the request-time quota check used, so an editor
	// collaborator's upload is billed to the owner rather than to themselves.
	_, file, err := s.pipeline.AttachOriginal(ctx, v.OwnerID, row.VideoID, video.UploadInput{
		Filename: sess.Filename,
		Reader:   reader,
	})
	if err != nil {
		switch {
		case errors.Is(err, video.ErrUnsupportedMedia):
			return safeerr.New("that file type is not an accepted video container")
		case errors.Is(err, video.ErrNotFound), errors.Is(err, video.ErrForbidden):
			return safeerr.New("the video no longer exists")
		default:
			return s.internalf("attach original", err)
		}
	}
	// Process is the shared publish pipeline: scan, probe, thumbnail,
	// storyboard, quarantine/schedule holds, and the transcode enqueue. A video
	// it decides to FAIL is not a job failure — the session completed and the
	// video row carries the outcome, exactly as when this ran in the request.
	if _, err := s.pipeline.Process(ctx, row.VideoID, file.StorageKey); err != nil {
		return s.internalf("process video", err)
	}
	return nil
}

// runReplace finalises a replace-purpose session (config-parity W14): the
// assembled bytes become a NEW source version of an already-published video.
// The feature gate and the published/no-replacement-in-flight state were
// checked when the completion was accepted; ReplaceSource re-checks ownership,
// the published state, and the media itself before anything is swapped.
func (s *Service) runReplace(ctx context.Context, row sqlcgen.ClaimDueUploadFinalizeJobsRow, sess sqlcgen.UploadSession, reader io.Reader) error {
	_, file, err := s.pipeline.ReplaceSource(ctx, sess.UserID, row.VideoID, video.UploadInput{
		Filename: sess.Filename,
		Reader:   reader,
	}, row.CanManage)
	if err != nil {
		var rejected *video.ReplaceRejectedError
		switch {
		case errors.As(err, &rejected):
			return safeerr.New(rejected.Reason)
		case errors.Is(err, video.ErrUnsupportedMedia):
			return safeerr.New("that file type is not an accepted video container")
		case errors.Is(err, video.ErrReplaceConflict):
			return safeerr.New("the video can no longer have its file replaced")
		case errors.Is(err, video.ErrNotFound), errors.Is(err, video.ErrForbidden):
			return safeerr.New("the video no longer exists")
		default:
			return s.internalf("replace source", err)
		}
	}
	if s.onReplaced != nil {
		s.onReplaced(ctx, row.VideoID, file.StorageKey)
	}
	return nil
}

// recordFailure reschedules with backoff, or dead-letters after the cap — and on
// dead-letter marks the SESSION failed with the same safe reason, because that
// is the only place a polling client can read it.
func (s *Service) recordFailure(ctx context.Context, row sqlcgen.ClaimDueUploadFinalizeJobsRow, cause error) {
	attempts := int(row.Attempts) + 1
	msg := cause.Error()
	if len(msg) > maxErrorLen {
		msg = msg[:maxErrorLen]
	}
	if attempts >= maxAttempts {
		if ferr := s.repo.FailUploadFinalizeJob(ctx, sqlcgen.FailUploadFinalizeJobParams{ID: row.ID, Error: msg}); ferr != nil {
			s.logger.Error("upload finalize: job could not be dead-lettered; the row stays 'running' until the lease sweep",
				"job_id", row.ID.String(), "error", ferr.Error())
		}
		if serr := s.uploads.MarkFailed(ctx, row.UploadID, msg); serr != nil {
			s.logger.Error("upload finalize: session could not be marked failed; the client's poll will hang until the sweep",
				"upload_id", row.UploadID.String(), "error", serr.Error())
		}
		return
	}
	if rerr := s.repo.RescheduleUploadFinalizeJob(ctx, sqlcgen.RescheduleUploadFinalizeJobParams{
		ID:            row.ID,
		NextAttemptAt: time.Now().UTC().Add(backoff(attempts)),
		Error:         msg,
	}); rerr != nil {
		s.logger.Error("upload finalize: job could not be rescheduled; the row stays 'running' until the lease sweep",
			"job_id", row.ID.String(), "error", rerr.Error())
	}
	// The session stays 'processing' between attempts: the work is still in
	// flight as far as the client is concerned, and saying otherwise would make
	// a transient storage blip look like a lost upload.
}

// internalf logs an unexpected internal error and returns a generic safe failure
// so nothing sensitive reaches the client-visible reason.
func (s *Service) internalf(where string, err error) error {
	s.logger.Warn("upload finalize failed", "stage", where, "error", err)
	return safeerr.New("the upload could not be processed")
}

// backoff is baseBackoff * 2^(attempts-1), capped at maxBackoff.
func backoff(attempts int) time.Duration {
	return retry.Backoff(attempts, baseBackoff, maxBackoff)
}
