// Package videoimport runs asynchronous URL import of a video's original file
// (fix_plan P2.2). POST /videos/:id/import enqueues an import_jobs row
// (migration 0059) and returns 202 instead of blocking the request on the
// SSRF-guarded fetch; an in-process worker claims due jobs, performs the
// same urlsafety-guarded fetch the endpoint used to do synchronously, stores
// the bytes via video.AttachOriginal, and finalises through video.Process —
// the identical post-upload pipeline (scan/probe/quarantine/transcode). Jobs
// retry with exponential backoff and dead-letter (state 'failed') after a
// bounded number of attempts, mirroring transcode_jobs.
//
// The stored job `error` is always a SAFE, client-visible reason (never a raw
// internal error or the attacker-controlled URL). It is HTTP-agnostic and
// testable with a fake repository + injected HTTP client.
package videoimport

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/urlsafety"
	"github.com/vidra/vidra-core/internal/video"
)

const (
	// maxAttempts is how many times a job is tried before dead-lettering.
	maxAttempts = 5
	// baseBackoff is the first retry delay; it doubles each attempt.
	baseBackoff = time.Minute
	// maxBackoff caps the exponential backoff.
	maxBackoff = time.Hour
	// fetchTimeout bounds the outbound fetch of one remote video.
	fetchTimeout = 60 * time.Second
	// maxErrorLen bounds the stored (client-visible) error string.
	maxErrorLen = 300
)

// Job states persisted on import_jobs.
const (
	StatePending = "pending"
	StateRunning = "running"
	StateDone    = "done"
	StateFailed  = "failed"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrNotFound means the video has no import job (GET status → 404).
	ErrNotFound = errors.New("videoimport: no import job")
	// ErrInvalidURL means the import URL is missing, malformed, non-http(s), or
	// a literal non-public address (enqueue → 422, before any fetch).
	ErrInvalidURL = errors.New("videoimport: invalid url")
)

// errImportTooLarge / errImportOverQuota flow up from the bounded body reader so
// the worker records the right safe reason.
var (
	errImportTooLarge  = errors.New("videoimport: fetched file exceeds the size limit")
	errImportOverQuota = errors.New("videoimport: fetched file exceeds the storage quota")
)

// Repository is the data access the import service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	EnqueueImportJob(ctx context.Context, arg sqlcgen.EnqueueImportJobParams) (sqlcgen.ImportJob, error)
	GetLatestImportJobByVideo(ctx context.Context, videoID uuid.UUID) (sqlcgen.ImportJob, error)
	ClaimDueImportJobs(ctx context.Context, limit int32) ([]sqlcgen.ClaimDueImportJobsRow, error)
	CompleteImportJob(ctx context.Context, id uuid.UUID) error
	RescheduleImportJob(ctx context.Context, arg sqlcgen.RescheduleImportJobParams) error
	FailImportJob(ctx context.Context, arg sqlcgen.FailImportJobParams) error
}

// Pipeline is the video ingest seam: store the fetched bytes as the video's
// original, then finalise through the shared publish pipeline. *video.Service
// satisfies it; a fake in tests.
type Pipeline interface {
	GetByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error)
	AttachOriginal(ctx context.Context, ownerID, videoID uuid.UUID, in video.UploadInput) (sqlcgen.Video, sqlcgen.VideoFile, error)
	Process(ctx context.Context, videoID uuid.UUID, originalKey string) (sqlcgen.Video, error)
}

// QuotaChecker reports a user's remaining storage headroom (limited=false means
// unlimited). Optional; when nil, imports are not quota-checked.
type QuotaChecker interface {
	Remaining(ctx context.Context, userID uuid.UUID) (remaining int64, limited bool, err error)
}

// Service holds the URL-import logic.
type Service struct {
	repo         Repository
	pipeline     Pipeline
	quota        QuotaChecker
	maxBytes     int64
	allowPrivate bool
	client       *http.Client // test seam; nil → build an SSRF-guarded client per fetch
	logger       *slog.Logger
}

// Option customises the Service.
type Option func(*Service)

// WithAllowPrivateFetch relaxes the SSRF guard's private/loopback block (dev/test
// only, from HTTP_IMPORT_ALLOW_PRIVATE_URLS). Scheme/host validation still applies.
func WithAllowPrivateFetch(allow bool) Option {
	return func(s *Service) { s.allowPrivate = allow }
}

// WithQuota wires per-user storage-quota enforcement on imports.
func WithQuota(q QuotaChecker) Option {
	return func(s *Service) { s.quota = q }
}

// WithHTTPClient injects the fetch client (tests use a plain client to reach a
// loopback origin the production guard would refuse). Production leaves it nil.
func WithHTTPClient(c *http.Client) Option {
	return func(s *Service) { s.client = c }
}

// WithLogger overrides the logger used for unexpected (non-client-facing)
// per-job errors.
func WithLogger(l *slog.Logger) Option {
	return func(s *Service) {
		if l != nil {
			s.logger = l
		}
	}
}

// NewService builds the import service. maxBytes is the UPLOAD_MAX_SIZE cap in
// bytes (0 = unbounded).
func NewService(repo Repository, pipeline Pipeline, maxBytes int64, opts ...Option) *Service {
	s := &Service{repo: repo, pipeline: pipeline, maxBytes: maxBytes, logger: slog.Default()}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// guard returns the SSRF guard configured for this service.
func (s *Service) guard() urlsafety.Guard {
	return urlsafety.Guard{AllowPrivate: s.allowPrivate}
}

// Enqueue validates the URL and queues an import for a video. The caller has
// already authorised the video (owner-only) so no fetch is issued on a
// non-owner's behalf. A single active import per video is enforced: while one is
// pending/running the in-flight job is returned unchanged (idempotent);
// otherwise a fresh job is created (so a finished/failed import can be retried).
// ErrInvalidURL when the URL is not a public http(s) URL.
func (s *Service) Enqueue(ctx context.Context, videoID uuid.UUID, rawURL string) (sqlcgen.ImportJob, error) {
	target, err := s.guard().ValidateURL(strings.TrimSpace(rawURL))
	if err != nil {
		return sqlcgen.ImportJob{}, ErrInvalidURL
	}
	job, err := s.repo.EnqueueImportJob(ctx, sqlcgen.EnqueueImportJobParams{
		VideoID: videoID,
		Url:     target.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// An active import already exists — return it (idempotent).
		return s.repo.GetLatestImportJobByVideo(ctx, videoID)
	}
	if err != nil {
		return sqlcgen.ImportJob{}, err
	}
	return job, nil
}

// LatestForVideo returns a video's most recent import job (GET status).
// ErrNotFound when the video was never imported.
func (s *Service) LatestForVideo(ctx context.Context, videoID uuid.UUID) (sqlcgen.ImportJob, error) {
	job, err := s.repo.GetLatestImportJobByVideo(ctx, videoID)
	if err != nil {
		return sqlcgen.ImportJob{}, ErrNotFound
	}
	return job, nil
}

// DrainJobs claims up to limit due jobs and runs each through the fetch +
// pipeline. On success the job completes; on failure it is rescheduled with
// backoff, or dead-lettered after maxAttempts. Returns the number completed.
// Only the claim-query error is returned — per-job outcomes are persisted.
// Intended to be called on a ticker by a single worker.
func (s *Service) DrainJobs(ctx context.Context, limit int) (int, error) {
	rows, err := s.repo.ClaimDueImportJobs(ctx, int32(limit))
	if err != nil {
		return 0, err
	}
	done := 0
	for _, row := range rows {
		if err := s.runImport(ctx, row); err != nil {
			s.recordFailure(ctx, row, err)
			continue
		}
		_ = s.repo.CompleteImportJob(ctx, row.ID)
		done++
	}
	return done, nil
}

// runImport performs one import: fetch the URL (SSRF-guarded), store the bytes
// as the video's original, and finalise. Every returned error carries a SAFE,
// client-visible message (mapped from the failure point); unexpected internal
// errors are logged and reported generically.
func (s *Service) runImport(ctx context.Context, row sqlcgen.ClaimDueImportJobsRow) error {
	v, err := s.pipeline.GetByID(ctx, row.VideoID)
	if err != nil {
		// The video was deleted out from under the job — permanent.
		return failf("the video no longer exists")
	}

	guard := s.guard()
	target, err := guard.ValidateURL(strings.TrimSpace(row.Url))
	if err != nil {
		return failf("the import URL is not a public http(s) URL")
	}

	// Resolve the owner's storage headroom before fetching (< 0 = unlimited).
	quotaRemaining := int64(-1)
	if s.quota != nil {
		remaining, limited, qerr := s.quota.Remaining(ctx, v.OwnerID)
		if qerr != nil {
			return s.internalf("import quota lookup", qerr)
		}
		if limited {
			quotaRemaining = remaining
		}
	}

	client := s.client
	if client == nil {
		client = guard.NewClient(fetchTimeout)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return failf("could not fetch the URL")
	}
	resp, err := client.Do(req)
	if err != nil {
		// Blocked address (SSRF guard), DNS failure, timeout, TLS error, etc. The
		// URL is never echoed back.
		return failf("could not fetch the URL")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return failf("the URL did not return a downloadable file")
	}
	if s.maxBytes > 0 && resp.ContentLength > s.maxBytes {
		return failf("the file is too large")
	}
	if quotaRemaining >= 0 && resp.ContentLength > quotaRemaining {
		return failf("storing the file would exceed your storage quota")
	}

	body := io.Reader(resp.Body)
	if s.maxBytes > 0 {
		body = &maxBytesReader{r: body, remaining: s.maxBytes, limitErr: errImportTooLarge}
	}
	if quotaRemaining >= 0 {
		body = &maxBytesReader{r: body, remaining: quotaRemaining, limitErr: errImportOverQuota}
	}

	_, file, err := s.pipeline.AttachOriginal(ctx, v.OwnerID, row.VideoID, video.UploadInput{
		Filename:    path.Base(target.Path),
		ContentType: resp.Header.Get("Content-Type"),
		Reader:      body,
	})
	if err != nil {
		switch {
		case errors.Is(err, errImportTooLarge):
			return failf("the file is too large")
		case errors.Is(err, errImportOverQuota):
			return failf("storing the file would exceed your storage quota")
		case errors.Is(err, video.ErrUnsupportedMedia):
			return failf("the URL is not an accepted video container")
		case errors.Is(err, video.ErrNotFound), errors.Is(err, video.ErrForbidden):
			return failf("the video no longer exists")
		default:
			return s.internalf("import attach original", err)
		}
	}
	if _, err := s.pipeline.Process(ctx, row.VideoID, file.StorageKey); err != nil {
		return s.internalf("import process", err)
	}
	return nil
}

// recordFailure reschedules with backoff, or dead-letters after the cap. The
// stored error is the safe message from runImport.
func (s *Service) recordFailure(ctx context.Context, row sqlcgen.ClaimDueImportJobsRow, cause error) {
	attempts := int(row.Attempts) + 1
	msg := cause.Error()
	if len(msg) > maxErrorLen {
		msg = msg[:maxErrorLen]
	}
	if attempts >= maxAttempts {
		_ = s.repo.FailImportJob(ctx, sqlcgen.FailImportJobParams{ID: row.ID, Error: msg})
		return
	}
	_ = s.repo.RescheduleImportJob(ctx, sqlcgen.RescheduleImportJobParams{
		ID:            row.ID,
		NextAttemptAt: time.Now().UTC().Add(backoff(attempts)),
		Error:         msg,
	})
}

// internalf logs an unexpected internal error (never the URL) and returns a
// generic safe failure so nothing sensitive reaches the client-visible error.
func (s *Service) internalf(where string, err error) error {
	s.logger.Warn("video import failed", "stage", where, "error", err)
	return failf("import failed")
}

// backoff is baseBackoff * 2^(attempts-1), capped at maxBackoff.
func backoff(attempts int) time.Duration {
	d := baseBackoff
	for i := 1; i < attempts; i++ {
		d *= 2
		if d >= maxBackoff {
			return maxBackoff
		}
	}
	return d
}

// failure carries a safe, client-visible import-failure reason.
type failure struct{ msg string }

func (f *failure) Error() string { return f.msg }

func failf(msg string) error { return &failure{msg: msg} }

// maxBytesReader caps how many bytes are read from the remote fetch, failing
// with limitErr once the cap is crossed (reads one past the cap to tell
// "exactly at" from "over").
type maxBytesReader struct {
	r         io.Reader
	remaining int64
	limitErr  error
}

func (m *maxBytesReader) Read(p []byte) (int, error) {
	if m.remaining < 0 {
		return 0, m.limitErr
	}
	if int64(len(p)) > m.remaining+1 {
		p = p[:m.remaining+1]
	}
	n, err := m.r.Read(p)
	m.remaining -= int64(n)
	if m.remaining < 0 {
		return n, m.limitErr
	}
	return n, err
}
