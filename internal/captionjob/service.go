// Package captionjob runs asynchronous auto-caption generation for a video
// (fix_plan P13, the Whisper adapter). POST /videos/:id/captions/auto enqueues a
// caption_jobs row (migration 0062) and returns 202; an in-process worker
// (started only when WHISPER_ENABLED) claims due jobs, hands the video's stored
// original to a Transcriber (internal/media.WhisperClient: extract audio → POST
// to the Whisper service → render WebVTT), upserts the resulting track through
// the SAME AddCaption path a manual upload uses (replace-by-language), and
// notifies the owner. Jobs retry with exponential backoff and dead-letter
// (state 'failed') after a bounded number of attempts, mirroring videoimport.
//
// The stored job `error` is always a SAFE, client-visible reason (never a raw
// internal error or the Whisper endpoint URL). The service is HTTP-agnostic and
// testable with a fake repository + a fake/httptest-backed transcriber.
package captionjob

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

const (
	// maxAttempts is how many times a job is tried before dead-lettering.
	maxAttempts = 5
	// baseBackoff is the first retry delay; it doubles each attempt.
	baseBackoff = time.Minute
	// maxBackoff caps the exponential backoff.
	maxBackoff = time.Hour
	// maxErrorLen bounds the stored (client-visible) error string.
	maxErrorLen = 300
	// autoCaptionLabel is the label stored on an auto-generated caption track so
	// the UI can distinguish it from a hand-uploaded one.
	autoCaptionLabel = "Auto-generated"
)

// Job states persisted on caption_jobs.
const (
	StatePending = "pending"
	StateRunning = "running"
	StateDone    = "done"
	StateFailed  = "failed"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrNotFound means the video has no auto-caption job (GET status → 404).
	ErrNotFound = errors.New("captionjob: no caption job")
	// ErrInvalidLanguage means the requested language is not a valid tag
	// (enqueue → 422, before any work).
	ErrInvalidLanguage = errors.New("captionjob: invalid language")
	// ErrJobActive means an auto-caption job is already pending/running for the
	// video (enqueue → 409).
	ErrJobActive = errors.New("captionjob: a caption job is already in progress")
	// ErrDisabled means auto-captioning is not enabled on this instance (enqueue
	// → 503).
	ErrDisabled = errors.New("captionjob: auto-captioning is disabled")
)

// Repository is the data access the service needs. *sqlcgen.Queries satisfies it
// directly; tests substitute an in-memory fake.
type Repository interface {
	EnqueueCaptionJob(ctx context.Context, arg sqlcgen.EnqueueCaptionJobParams) (sqlcgen.CaptionJob, error)
	GetLatestCaptionJobByVideo(ctx context.Context, videoID uuid.UUID) (sqlcgen.CaptionJob, error)
	ClaimDueCaptionJobs(ctx context.Context, limit int32) ([]sqlcgen.ClaimDueCaptionJobsRow, error)
	CompleteCaptionJob(ctx context.Context, id uuid.UUID) error
	RescheduleCaptionJob(ctx context.Context, arg sqlcgen.RescheduleCaptionJobParams) error
	FailCaptionJob(ctx context.Context, arg sqlcgen.FailCaptionJobParams) error
}

type progressRepository interface {
	SetCaptionJobProgress(ctx context.Context, arg sqlcgen.SetCaptionJobProgressParams) error
}

// Transcriber turns a stored video's audio into a WebVTT caption track.
// *media.WhisperClient satisfies it; tests use a fake or an httptest-backed one.
type Transcriber interface {
	Transcribe(ctx context.Context, sourceKey, languageHint string) ([]byte, error)
}

// VideoStore is the video seam: resolve a video's owner + original storage key,
// then upsert the produced caption through the owner-scoped AddCaption path.
// *video.Service satisfies it.
type VideoStore interface {
	GetByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error)
	OriginalFileKey(ctx context.Context, videoID uuid.UUID) (string, error)
	AddCaption(ctx context.Context, ownerID, videoID uuid.UUID, in video.CaptionInput) (sqlcgen.Caption, error)
}

// Notifier tells a video's owner their auto-caption is ready.
// *notification.Service satisfies it; it is optional (nil = no notification).
type Notifier interface {
	NotifyCaptionReady(ctx context.Context, recipientID, videoID uuid.UUID) error
}

// Service holds the auto-caption job logic.
type Service struct {
	repo            Repository
	videos          VideoStore
	transcriber     Transcriber
	notifier        Notifier
	enabled         bool
	enabledFn       func() bool // when set, supersedes enabled (runtime overlay), resolved per call
	defaultLanguage string
	logger          *slog.Logger
}

// Option customises the Service.
type Option func(*Service)

// WithEnabled sets whether auto-captioning is turned on (WHISPER_ENABLED). When
// false, Enqueue returns ErrDisabled and DrainJobs is a no-op.
func WithEnabled(enabled bool) Option {
	return func(s *Service) { s.enabled = enabled }
}

// WithEnabledFunc makes the gate dynamic (transcription_enabled, config-parity
// W8): f is resolved per enqueue and per worker tick so an admin can toggle
// auto-captioning without a restart (the worker stays constructed and simply
// picks nothing up while off). When set it supersedes WithEnabled; wire f to
// fold in the boot capability (WHISPER_ENABLED/WHISPER_ENDPOINT) so the
// effective value is always settingAND(whisper configured).
func WithEnabledFunc(f func() bool) Option {
	return func(s *Service) { s.enabledFn = f }
}

// WithDefaultLanguage sets the caption language used when a request omits one
// (and the hint passed to the transcriber). Empty values are ignored.
func WithDefaultLanguage(lang string) Option {
	return func(s *Service) {
		if video.ValidCaptionLanguage(lang) {
			s.defaultLanguage = lang
		}
	}
}

// WithNotifier wires owner notification on completion (optional).
func WithNotifier(n Notifier) Option {
	return func(s *Service) { s.notifier = n }
}

// WithLogger overrides the logger used for unexpected (non-client-facing) errors.
func WithLogger(l *slog.Logger) Option {
	return func(s *Service) {
		if l != nil {
			s.logger = l
		}
	}
}

// NewService builds the auto-caption job service. transcriber may be nil when
// auto-captioning is disabled (the worker never runs then).
func NewService(repo Repository, videos VideoStore, transcriber Transcriber, opts ...Option) *Service {
	s := &Service{
		repo:            repo,
		videos:          videos,
		transcriber:     transcriber,
		defaultLanguage: "en",
		logger:          slog.Default(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Enabled reports whether auto-captioning is EFFECTIVELY on: the runtime
// provider when wired (which folds in the boot capability), else the static
// boot flag.
func (s *Service) Enabled() bool {
	if s.enabledFn != nil {
		return s.enabledFn()
	}
	return s.enabled
}

// Enqueue queues an auto-caption job for a video. The caller has already
// authorised the video (owner-only). language is optional; empty uses the
// configured default. A single active job runs per video: while one is
// pending/running, a re-request returns ErrJobActive (→ 409). ErrDisabled when
// auto-captioning is off; ErrInvalidLanguage when the language tag is malformed.
func (s *Service) Enqueue(ctx context.Context, videoID uuid.UUID, language string) (sqlcgen.CaptionJob, error) {
	if !s.Enabled() {
		return sqlcgen.CaptionJob{}, ErrDisabled
	}
	lang := language
	if lang == "" {
		lang = s.defaultLanguage
	}
	if !video.ValidCaptionLanguage(lang) {
		return sqlcgen.CaptionJob{}, ErrInvalidLanguage
	}
	job, err := s.repo.EnqueueCaptionJob(ctx, sqlcgen.EnqueueCaptionJobParams{
		VideoID:  videoID,
		Language: normalizeLanguage(lang),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// An active job already exists — the request conflicts.
		return sqlcgen.CaptionJob{}, ErrJobActive
	}
	if err != nil {
		return sqlcgen.CaptionJob{}, err
	}
	return job, nil
}

// LatestForVideo returns a video's most recent auto-caption job (GET status).
// ErrNotFound when none was ever requested.
func (s *Service) LatestForVideo(ctx context.Context, videoID uuid.UUID) (sqlcgen.CaptionJob, error) {
	job, err := s.repo.GetLatestCaptionJobByVideo(ctx, videoID)
	if err != nil {
		return sqlcgen.CaptionJob{}, ErrNotFound
	}
	return job, nil
}

// DrainJobs claims up to limit due jobs and runs each through transcription +
// caption upsert. On success the job completes; on failure it is rescheduled
// with backoff, or dead-lettered after maxAttempts. Returns the number
// completed. Only the claim-query error is returned — per-job outcomes are
// persisted. Intended to be called on a ticker by a single worker. A no-op when
// auto-captioning is disabled.
func (s *Service) DrainJobs(ctx context.Context, limit int) (int, error) {
	if !s.Enabled() {
		return 0, nil
	}
	rows, err := s.repo.ClaimDueCaptionJobs(ctx, int32(limit))
	if err != nil {
		return 0, err
	}
	done := 0
	for _, row := range rows {
		if err := s.runJob(ctx, row); err != nil {
			s.recordFailure(ctx, row, err)
			continue
		}
		_ = s.repo.CompleteCaptionJob(ctx, row.ID)
		done++
	}
	return done, nil
}

// runJob performs one job: locate the video's original, transcribe it, and store
// the caption through the owner-scoped AddCaption path, then notify the owner.
// Every returned error carries a SAFE, client-visible message.
func (s *Service) runJob(ctx context.Context, row sqlcgen.ClaimDueCaptionJobsRow) error {
	v, err := s.videos.GetByID(ctx, row.VideoID)
	if err != nil {
		// The video was deleted out from under the job — permanent.
		return failf("the video no longer exists")
	}
	sourceKey, err := s.videos.OriginalFileKey(ctx, row.VideoID)
	if err != nil {
		return failf("the video has no processed media to caption")
	}
	s.setProgress(ctx, row.ID, "transcribing", 20)

	vtt, err := s.transcriber.Transcribe(ctx, sourceKey, row.Language)
	if err != nil {
		// Extraction / transcription failure (ffmpeg, network, bad response). The
		// endpoint URL is never echoed back.
		return s.internalf("whisper transcription", err)
	}
	s.setProgress(ctx, row.ID, "storing", 90)

	if _, err := s.videos.AddCaption(ctx, v.OwnerID, row.VideoID, video.CaptionInput{
		Language: row.Language,
		Label:    autoCaptionLabel,
		Reader:   bytes.NewReader(vtt),
	}); err != nil {
		switch {
		case errors.Is(err, video.ErrInvalidCaption):
			return failf("the transcription did not produce a valid caption")
		case errors.Is(err, video.ErrNotFound), errors.Is(err, video.ErrForbidden):
			return failf("the video no longer exists")
		default:
			return s.internalf("caption upsert", err)
		}
	}

	// Owner notification is best-effort: a failure must not fail the job (the
	// caption is already stored).
	if s.notifier != nil {
		if err := s.notifier.NotifyCaptionReady(ctx, v.OwnerID, row.VideoID); err != nil {
			s.logger.Warn("caption-ready notification failed", "video_id", row.VideoID.String(), "error", err)
		}
	}
	return nil
}

func (s *Service) setProgress(ctx context.Context, id uuid.UUID, stage string, percent int16) {
	if repo, ok := s.repo.(progressRepository); ok {
		_ = repo.SetCaptionJobProgress(ctx, sqlcgen.SetCaptionJobProgressParams{
			ID: id, Stage: stage, ProgressPercent: percent,
		})
	}
}

// recordFailure reschedules with backoff, or dead-letters after the cap. The
// stored error is the safe message from runJob.
func (s *Service) recordFailure(ctx context.Context, row sqlcgen.ClaimDueCaptionJobsRow, cause error) {
	attempts := int(row.Attempts) + 1
	msg := cause.Error()
	if len(msg) > maxErrorLen {
		msg = msg[:maxErrorLen]
	}
	if attempts >= maxAttempts {
		_ = s.repo.FailCaptionJob(ctx, sqlcgen.FailCaptionJobParams{ID: row.ID, Error: msg})
		return
	}
	_ = s.repo.RescheduleCaptionJob(ctx, sqlcgen.RescheduleCaptionJobParams{
		ID:            row.ID,
		NextAttemptAt: time.Now().UTC().Add(backoff(attempts)),
		Error:         msg,
	})
}

// internalf logs an unexpected internal error (never the endpoint URL) and
// returns a generic safe failure so nothing sensitive reaches the client-visible
// error.
func (s *Service) internalf(where string, err error) error {
	s.logger.Warn("auto-caption failed", "stage", where, "error", err)
	return failf("auto-captioning failed")
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

// normalizeLanguage trims the language tag (validated by the caller).
func normalizeLanguage(lang string) string {
	return strings.TrimSpace(lang)
}

// failure carries a safe, client-visible failure reason.
type failure struct{ msg string }

func (f *failure) Error() string { return f.msg }

func failf(msg string) error { return &failure{msg: msg} }
