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
	"net/url"
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
	// ErrInvalidResolver means the requested resolver is not auto|direct|ytdlp
	// (enqueue → 422).
	ErrInvalidResolver = errors.New("videoimport: invalid resolver")
	// ErrResolverDisabled means the requested resolver is turned off on this
	// instance (enqueue → 503, e.g. resolver=ytdlp while YTDLP_IMPORT_ENABLED is
	// false).
	ErrResolverDisabled = errors.New("videoimport: resolver disabled")
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
	SetImportJobStage(ctx context.Context, arg sqlcgen.SetImportJobStageParams) error
	SetImportJobResolver(ctx context.Context, arg sqlcgen.SetImportJobResolverParams) error
}

// Pipeline is the video ingest seam: store the fetched bytes as the video's
// original, then finalise through the shared publish pipeline. *video.Service
// satisfies it; a fake in tests.
type Pipeline interface {
	GetByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error)
	AttachOriginal(ctx context.Context, ownerID, videoID uuid.UUID, in video.UploadInput) (sqlcgen.Video, sqlcgen.VideoFile, error)
	Process(ctx context.Context, videoID uuid.UUID, originalKey string) (sqlcgen.Video, error)
	// PrefillMetadata fills a draft's title/description from an extractor probe,
	// but only fields the user left EMPTY (never overwriting what they typed).
	PrefillMetadata(ctx context.Context, videoID uuid.UUID, title, description string) error
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
	maxBytesFn   func() int64 // when set, supersedes maxBytes (live overlay), resolved per job
	allowPrivate bool
	client       *http.Client // test seam; nil → build an SSRF-guarded client per fetch
	logger       *slog.Logger

	// ytdlp is the sandboxed platform extractor. nil = disabled (the default):
	// an explicit resolver=ytdlp is refused at enqueue (503) and resolver=auto
	// never falls back to it.
	ytdlp     Extractor
	ytdlpWork string // parent dir for per-job ytdlp workdirs (os.TempDir when "")
	// ytdlpGate is the runtime admin toggle over the wired extractor
	// (import_http_enabled, config-parity W8). nil = always allowed; the boot
	// capability (ytdlp != nil) still applies either way.
	ytdlpGate func() bool
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

// WithMaxBytesFunc makes the per-file size cap dynamic: f is resolved once per
// import job (at fetch time) so an admin can retune the upload-size overlay at
// runtime. When set it supersedes the constructor's maxBytes. A running download
// keeps the cap it started with; only new jobs see a change.
func WithMaxBytesFunc(f func() int64) Option {
	return func(s *Service) { s.maxBytesFn = f }
}

// effectiveMaxBytes resolves the current per-file size cap (0 = unbounded): the
// live overlay value when a provider is wired, else the static constructor value.
func (s *Service) effectiveMaxBytes() int64 {
	if s.maxBytesFn != nil {
		return s.maxBytesFn()
	}
	return s.maxBytes
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

// WithYtdlp enables the sandboxed yt-dlp platform-import resolver. Wire it only
// when YTDLP_IMPORT_ENABLED is on. workRoot is the parent for per-job private
// workdirs (empty → os.TempDir()).
func WithYtdlp(ext Extractor, workRoot string) Option {
	return func(s *Service) {
		s.ytdlp = ext
		s.ytdlpWork = workRoot
	}
}

// WithYtdlpGate wires the runtime admin toggle over the platform resolver
// (import_http_enabled, config-parity W8): f is consulted per enqueue/resolve
// so an admin can turn the yt-dlp import path off without a restart. It can
// only narrow availability — with no extractor wired (boot capability off) the
// resolver stays disabled regardless of f.
func WithYtdlpGate(f func() bool) Option {
	return func(s *Service) { s.ytdlpGate = f }
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

// YtdlpEnabled reports whether the sandboxed platform-import resolver is
// EFFECTIVELY available: wired at boot (YTDLP_IMPORT_ENABLED) AND allowed by
// the runtime import_http_enabled gate when one is wired. The handler uses it
// to refuse an explicit resolver=ytdlp request up front rather than enqueue a
// job that can only fail.
func (s *Service) YtdlpEnabled() bool {
	return s.ytdlp != nil && (s.ytdlpGate == nil || s.ytdlpGate())
}

// normalizeResolver validates the requested resolver and applies the disabled
// policy. Empty defaults to auto. An explicit ytdlp while disabled is
// ErrResolverDisabled (503); an unknown value is ErrInvalidResolver (422).
func (s *Service) normalizeResolver(requested string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(requested)) {
	case "", ResolverAuto:
		return ResolverAuto, nil
	case ResolverDirect:
		return ResolverDirect, nil
	case ResolverYtdlp:
		if !s.YtdlpEnabled() {
			return "", ErrResolverDisabled
		}
		return ResolverYtdlp, nil
	default:
		return "", ErrInvalidResolver
	}
}

// Enqueue validates the URL + resolver and queues an import for a video. The
// caller has already authorised the video (owner-only) so no fetch is issued on
// a non-owner's behalf. A single active import per video is enforced: while one
// is pending/running the in-flight job is returned unchanged (idempotent);
// otherwise a fresh job is created (so a finished/failed import can be retried).
// requestedResolver is auto|direct|ytdlp (empty = auto). Errors: ErrInvalidURL,
// ErrInvalidResolver, ErrResolverDisabled.
func (s *Service) Enqueue(ctx context.Context, videoID uuid.UUID, rawURL, requestedResolver string) (sqlcgen.ImportJob, error) {
	target, err := s.guard().ValidateURL(strings.TrimSpace(rawURL))
	if err != nil {
		return sqlcgen.ImportJob{}, ErrInvalidURL
	}
	resolver, err := s.normalizeResolver(requestedResolver)
	if err != nil {
		return sqlcgen.ImportJob{}, err
	}
	job, err := s.repo.EnqueueImportJob(ctx, sqlcgen.EnqueueImportJobParams{
		VideoID:  videoID,
		Url:      target.String(),
		Resolver: resolver,
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

// runImport performs one import: pick the resolver (SSRF-guarded), fetch the
// bytes, store them as the video's original, and finalise. Every returned error
// carries a SAFE, client-visible message (mapped from the failure point);
// unexpected internal errors are logged and reported generically. Coarse
// progress is written to import_jobs.stage as it advances so the UI can show
// honest status.
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

	// Pick the concrete resolver (auto → direct|ytdlp), recording honest stages.
	res, err := s.selectResolver(ctx, guard, row, target)
	if err != nil {
		return err // already a safe failure
	}

	s.setStage(ctx, row.ID, StageDownloading)
	media, err := res.resolve(ctx, target)
	if err != nil {
		return err // already a safe failure
	}
	defer func() { _ = media.body.Close() }()

	// Up-front size/quota rejection when the size is known (direct Content-Length
	// or a yt-dlp file stat) — avoids streaming bytes we will only discard. The
	// cap is resolved once per job so a runtime overlay change is honoured.
	maxBytes := s.effectiveMaxBytes()
	if maxBytes > 0 && media.knownSize > maxBytes {
		return failf("the file is too large")
	}
	if quotaRemaining >= 0 && media.knownSize > quotaRemaining {
		return failf("storing the file would exceed your storage quota")
	}

	body := io.Reader(media.body)
	if maxBytes > 0 {
		body = &maxBytesReader{r: body, remaining: maxBytes, limitErr: errImportTooLarge}
	}
	if quotaRemaining >= 0 {
		body = &maxBytesReader{r: body, remaining: quotaRemaining, limitErr: errImportOverQuota}
	}

	s.setStage(ctx, row.ID, StageProcessing)
	_, file, err := s.pipeline.AttachOriginal(ctx, v.OwnerID, row.VideoID, video.UploadInput{
		Filename:    media.filename,
		ContentType: media.contentType,
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

	// Best-effort draft prefill from platform metadata (fills only empty fields;
	// a failure never fails the import — the bytes already landed).
	if media.title != "" || media.description != "" {
		if perr := s.pipeline.PrefillMetadata(ctx, row.VideoID, media.title, media.description); perr != nil {
			s.logger.Warn("video import metadata prefill failed", "video", row.VideoID, "error", perr)
		}
	}

	if _, err := s.pipeline.Process(ctx, row.VideoID, file.StorageKey); err != nil {
		return s.internalf("import process", err)
	}
	return nil
}

// selectResolver maps a job's requested resolver to a concrete implementation.
// For an 'auto' request it probes the URL (recognised extension → direct; else a
// guarded HEAD content-type; else the yt-dlp extractor when enabled) and rewrites
// the stored resolver so the echoed value is always what actually ran.
func (s *Service) selectResolver(ctx context.Context, guard urlsafety.Guard, row sqlcgen.ClaimDueImportJobsRow, target *url.URL) (resolver, error) {
	concrete := row.Resolver
	switch row.Resolver {
	case ResolverDirect:
		concrete = ResolverDirect
	case ResolverYtdlp:
		if !s.YtdlpEnabled() {
			return nil, failf("platform import is not enabled")
		}
		concrete = ResolverYtdlp
	case ResolverAuto:
		s.setStage(ctx, row.ID, StageResolving)
		concrete = s.probeAuto(ctx, guard, target)
		if concrete == "" {
			return nil, failf("could not import from this URL")
		}
		// Rewrite the stored resolver to what actually runs (honest view).
		_ = s.repo.SetImportJobResolver(ctx, sqlcgen.SetImportJobResolverParams{
			ID: row.ID, Resolver: concrete, Stage: StageResolving,
		})
	default:
		return nil, failf("could not import from this URL")
	}
	return s.buildResolver(guard, concrete)
}

// buildResolver constructs the concrete resolver.
func (s *Service) buildResolver(guard urlsafety.Guard, concrete string) (resolver, error) {
	if concrete == ResolverYtdlp {
		if !s.YtdlpEnabled() {
			return nil, failf("platform import is not enabled")
		}
		return &ytdlpResolver{ext: s.ytdlp, workRoot: s.ytdlpWork}, nil
	}
	return &directResolver{client: s.httpClient(guard)}, nil
}

// probeAuto decides between the direct and ytdlp resolvers for an 'auto' request
// without downloading anything: a recognised video extension is direct outright;
// otherwise a bounded guarded HEAD content-type decides; anything else routes to
// the platform extractor when enabled, else back to a direct attempt (which
// fails safely if the body is not an accepted container).
func (s *Service) probeAuto(ctx context.Context, guard urlsafety.Guard, target *url.URL) string {
	if _, ok := video.AcceptedVideoExt(target.Path); ok {
		return ResolverDirect
	}
	if ct := s.probeContentType(ctx, guard, target); ct != "" && isAcceptedVideoContentType(ct) {
		return ResolverDirect
	}
	if s.YtdlpEnabled() {
		return ResolverYtdlp
	}
	return ResolverDirect
}

// probeContentType issues a bounded, SSRF-guarded HEAD and returns the response
// Content-Type ("" on any failure). It never surfaces the URL or an error.
func (s *Service) probeContentType(ctx context.Context, guard urlsafety.Guard, target *url.URL) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, target.String(), nil)
	if err != nil {
		return ""
	}
	resp, err := s.httpClient(guard).Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	return resp.Header.Get("Content-Type")
}

// httpClient returns the injected test client, or a fresh SSRF-guarded client.
func (s *Service) httpClient(guard urlsafety.Guard) *http.Client {
	if s.client != nil {
		return s.client
	}
	return guard.NewClient(fetchTimeout)
}

// setStage records coarse progress (best-effort; a bookkeeping write failure
// never fails the import).
func (s *Service) setStage(ctx context.Context, id uuid.UUID, stage string) {
	_ = s.repo.SetImportJobStage(ctx, sqlcgen.SetImportJobStageParams{ID: id, Stage: stage})
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
