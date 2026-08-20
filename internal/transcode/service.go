// Package transcode runs the HLS transcoding pipeline around a durable
// PostgreSQL job queue (migration 0039, mirroring federation_deliveries): the
// publish transition enqueues a job best-effort, an in-process worker claims
// due jobs, runs the ffmpeg-backed transcoder (internal/media.HLSTranscoder),
// stores the resulting renditions + playlists, and marks the video's streaming
// playlist ready. Failures retry with exponential backoff and dead-letter
// after a bounded number of attempts. It is HTTP-agnostic and testable with a
// fake repository + transcoder.
package transcode

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/workerpool"
)

const (
	// maxAttempts is how many times a job is tried before it is dead-lettered
	// (state 'failed').
	maxAttempts = 5
	// baseBackoff is the first retry delay; it doubles each attempt.
	baseBackoff = time.Minute
	// maxBackoff caps the exponential backoff.
	maxBackoff = time.Hour
	// maxLastErrorLen bounds the stored last_error string.
	maxLastErrorLen = 500
	// bookkeepingTimeout bounds the queue-state writes that follow a job (see
	// DrainJobs). They deliberately outlive the request/worker cancellation that
	// ended the job, so they need a deadline of their own — otherwise a wedged
	// database would hold shutdown open indefinitely. It must stay comfortably
	// under HTTP_SHUTDOWN_TIMEOUT (20s) so a clean SIGTERM still drains.
	bookkeepingTimeout = 10 * time.Second
)

const (
	TargetAll      = "all"
	TargetHLS      = "hls"
	TargetWebVideo = "web_video"
)

// Playlist states persisted on streaming_playlists.
const (
	PlaylistReady  = "ready"
	PlaylistFailed = "failed"
)

// Repository is the data access the transcode service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	EnqueueTranscodeJob(ctx context.Context, arg sqlcgen.EnqueueTranscodeJobParams) error
	ClaimDueTranscodeJobs(ctx context.Context, limit int32) ([]sqlcgen.ClaimDueTranscodeJobsRow, error)
	CompleteTranscodeJob(ctx context.Context, id uuid.UUID) error
	RescheduleTranscodeJob(ctx context.Context, arg sqlcgen.RescheduleTranscodeJobParams) error
	FailTranscodeJob(ctx context.Context, arg sqlcgen.FailTranscodeJobParams) error
	HasLiveTranscodeJob(ctx context.Context, videoID uuid.UUID) (bool, error)
	UpsertStreamingPlaylist(ctx context.Context, arg sqlcgen.UpsertStreamingPlaylistParams) (sqlcgen.StreamingPlaylist, error)
	GetStreamingPlaylist(ctx context.Context, videoID uuid.UUID) (sqlcgen.StreamingPlaylist, error)
	DeleteStreamingPlaylist(ctx context.Context, videoID uuid.UUID) error
	CreateVideoRendition(ctx context.Context, arg sqlcgen.CreateVideoRenditionParams) (sqlcgen.VideoRendition, error)
	DeleteVideoRenditions(ctx context.Context, videoID uuid.UUID) error
	ListVideoRenditions(ctx context.Context, videoID uuid.UUID) ([]sqlcgen.VideoRendition, error)
	CreateVideoFile(ctx context.Context, arg sqlcgen.CreateVideoFileParams) (sqlcgen.VideoFile, error)
	DeleteVideoFilesByVideoAndKind(ctx context.Context, arg sqlcgen.DeleteVideoFilesByVideoAndKindParams) error
}

// Transcoder produces the HLS ladder for a stored original. It is the seam the
// worker runs jobs through — media.HLSTranscoder in production, a fake in tests.
type Transcoder interface {
	Transcode(ctx context.Context, videoID uuid.UUID, sourceKey string) (media.HLSResult, error)
}

// TargetTranscoder is implemented by the production media transcoder. It lets
// manual operator actions rebuild HLS and Web Video outputs independently and
// reports one progress stream per resolution. The narrow Transcoder interface
// remains for test doubles and backwards-compatible adapters.
//
// Probe is separate from the two encode methods so a job running both targets
// reads the source's metadata ONCE and hands it to each. On a backend with no
// local paths (S3) a probe streams the whole source to a temp file, so the
// previous shape — where each target probed independently — spent two full
// source downloads answering the same question.
type TargetTranscoder interface {
	Probe(ctx context.Context, sourceKey string) (media.Metadata, error)
	TranscodeHLS(ctx context.Context, videoID uuid.UUID, sourceKey string, md media.Metadata, progress media.ProgressFunc) (media.HLSResult, error)
	TranscodeWebVideos(ctx context.Context, videoID uuid.UUID, sourceKey string, md media.Metadata, progress media.ProgressFunc) ([]media.WebVideoResult, error)
}

type stepRepository interface {
	UpsertTranscodeStep(ctx context.Context, arg sqlcgen.UpsertTranscodeStepParams) error
}

type targetRunError struct {
	target string
	err    error
}

func (e *targetRunError) Error() string { return e.err.Error() }
func (e *targetRunError) Unwrap() error { return e.err }

// ErrNoTranscoder means DrainJobs was called on a read-only service (no
// Transcoder wired) — the worker should not be running in that configuration.
var ErrNoTranscoder = errors.New("transcode: no transcoder configured")

// Service holds the transcoding pipeline logic. transcoder may be nil for a
// read-only service (playlist/rendition lookups for serving); Enqueue and the
// lookups work regardless.
type Service struct {
	repo       Repository
	transcoder Transcoder
	onComplete func(ctx context.Context, videoID uuid.UUID)
	onFail     func(ctx context.Context, videoID uuid.UUID)
	// enabledFn is the runtime transcoding_enabled gate (config-parity W10),
	// consulted at BOTH job enqueue and worker pickup — never at construction
	// (the boot-baked-worker gotcha: the worker is always wired so a runtime
	// re-enable needs no restart). nil = enabled.
	enabledFn func() bool
	// concurrencyFn is the runtime transcoding_concurrency value, resolved per
	// DrainJobs call (per worker tick) so a change applies without a restart.
	// nil = 1 (the pre-W10 sequential behavior).
	concurrencyFn func() int64
}

// Option configures the transcode service.
type Option func(*Service)

// WithCompletionHook registers a best-effort callback fired after a transcode job
// completes successfully (the video's HLS tree + VP9/WebM alternate are now
// stored). The IPFS mirror uses it to add+pin the finalized HLS directory (fix_plan
// P19.4). The hook runs inline on the worker goroutine; it must not block and its
// failures must not fail the job (the transcode already succeeded).
func WithCompletionHook(fn func(ctx context.Context, videoID uuid.UUID)) Option {
	return func(s *Service) { s.onComplete = fn }
}

// WithFailureHook registers a best-effort callback fired after a transcode job is
// permanently dead-lettered (state 'failed' after maxAttempts). The
// publish-after-transcode hold uses it to release a held video even when its
// transcode never completes — a video hidden forever is worse than one served
// from its (playable) original. Runs inline on the worker goroutine; it must not
// block and its failures must not affect the queue.
func WithFailureHook(fn func(ctx context.Context, videoID uuid.UUID)) Option {
	return func(s *Service) { s.onFail = fn }
}

// WithEnabledFunc wires the runtime transcoding_enabled gate (config-parity
// W10). f is consulted per Enqueue and per DrainJobs call — the worker itself
// stays constructed and ticking, so an admin can flip transcoding on/off
// without a restart. While off, Enqueue is a silent no-op (the upload stays
// playable via the retained original, served progressively) and the worker
// claims nothing (pending jobs wait, they are not failed).
func WithEnabledFunc(f func() bool) Option {
	return func(s *Service) { s.enabledFn = f }
}

// WithConcurrencyFunc wires the runtime transcoding_concurrency value
// (config-parity W10). f is resolved once per DrainJobs call (per worker
// tick), clamped to [1, workerpool.MaxConcurrency]; running jobs keep the
// parallelism they started with.
func WithConcurrencyFunc(f func() int64) Option {
	return func(s *Service) { s.concurrencyFn = f }
}

// Enabled reports the effective runtime transcoding gate (true when no
// provider is wired — the pre-W10 behavior).
func (s *Service) Enabled() bool {
	return s.enabledFn == nil || s.enabledFn()
}

// Capable reports whether this service can actually run jobs (a transcoder is
// wired — ffmpeg/ffprobe present at boot). The GET /instance features block
// ANDs it with the runtime setting so clients see EFFECTIVE availability.
func (s *Service) Capable() bool { return s.transcoder != nil }

// Concurrency is the effective worker parallelism for the next drain, clamped
// to [1, workerpool.MaxConcurrency] (1 when no provider is wired).
func (s *Service) Concurrency() int {
	if s.concurrencyFn == nil {
		return 1
	}
	return workerpool.Clamp(s.concurrencyFn())
}

// NewService builds the transcode service. transcoder may be nil when only the
// read side (playlist/rendition lookups) is needed.
func NewService(repo Repository, transcoder Transcoder, opts ...Option) *Service {
	s := &Service{repo: repo, transcoder: transcoder}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Enqueue queues a transcode job for a video's stored original. Idempotent per
// live job: while a pending/running job exists for the video, re-enqueueing is
// a no-op (partial unique index + ON CONFLICT DO NOTHING). While the runtime
// transcoding_enabled gate is off, Enqueue is a silent no-op: no job row is
// written and the publish proceeds — the video stays playable via the retained
// original (progressive Range serving), matching the pre-existing
// transcoding-disabled behavior.
func (s *Service) Enqueue(ctx context.Context, videoID uuid.UUID, sourceKey string) error {
	return s.EnqueueTarget(ctx, videoID, sourceKey, TargetAll)
}

// EnqueueTarget queues a full initial transcode or one operator-requested
// output class. sourceKey must be the video's retained original key; callers
// never derive it from an HLS/Web Video output, preventing generational quality
// loss on repeated manual runs.
func (s *Service) EnqueueTarget(ctx context.Context, videoID uuid.UUID, sourceKey, target string) error {
	if !s.Enabled() {
		return nil
	}
	if target != TargetAll && target != TargetHLS && target != TargetWebVideo {
		return errors.New("transcode: invalid target")
	}
	return s.repo.EnqueueTranscodeJob(ctx, sqlcgen.EnqueueTranscodeJobParams{
		VideoID:       videoID,
		SourceKey:     sourceKey,
		TranscodeType: target,
	})
}

// HasLiveJob reports whether a pending/running transcode job exists for the
// video. The replacement flow (config-parity W14) refuses to start while one
// does: Enqueue is idempotent per live job, so a replacement accepted now
// would never be transcoded. Errors are reported as busy (fail-safe: the
// caller answers 409 and the client retries).
func (s *Service) HasLiveJob(ctx context.Context, videoID uuid.UUID) bool {
	live, err := s.repo.HasLiveTranscodeJob(ctx, videoID)
	return err != nil || live
}

// LiveJob is HasLiveJob with the lookup error surfaced, so callers choose their
// own failure posture. HasLiveJob's fail-busy default is right for ADMISSION
// decisions (refuse a replacement, enter a hold) but wrong for RELEASE
// decisions: treating a transient DB error at last-job completion as "still
// busy" would suppress a publish-after-transcode release until the stuck
// sweeper. Release call sites treat "undetermined" as releasable instead — the
// state-guarded release CAS makes an over-eager attempt harmless.
func (s *Service) LiveJob(ctx context.Context, videoID uuid.UUID) (bool, error) {
	return s.repo.HasLiveTranscodeJob(ctx, videoID)
}

// Invalidate drops a video's streaming playlist + derivative rows (config-
// parity W14): when a source replacement lands while transcoding is
// unavailable, old HLS and progressive Web Videos must stop serving the
// superseded content. Playback falls back to the new original; the orphaned
// generation's blobs are mediagc's to collect.
func (s *Service) Invalidate(ctx context.Context, videoID uuid.UUID) error {
	if err := s.repo.DeleteVideoRenditions(ctx, videoID); err != nil {
		return err
	}
	for _, kind := range []string{"rendition", "webm"} {
		if err := s.repo.DeleteVideoFilesByVideoAndKind(ctx, sqlcgen.DeleteVideoFilesByVideoAndKindParams{
			VideoID: videoID,
			Kind:    kind,
		}); err != nil {
			return err
		}
	}
	return s.repo.DeleteStreamingPlaylist(ctx, videoID)
}

// DrainJobs claims up to limit due jobs and runs each through the transcoder:
// on success the video's renditions are replaced, its streaming playlist is
// marked ready, and the job completes; on failure the job is rescheduled with
// exponential backoff, or dead-lettered (and the playlist marked failed) after
// maxAttempts. Returns the number completed. Only the claim-query error is
// returned — per-job failures are persisted in the queue, not surfaced.
// Intended to be called on a ticker by a single worker goroutine; the claimed
// batch runs on a bounded pool sized by the runtime transcoding_concurrency
// value, resolved per call (config-parity W10), so an admin change applies on
// the next tick without a restart. While the runtime transcoding_enabled gate
// is off, nothing is claimed (pending jobs wait; they are not failed).
func (s *Service) DrainJobs(ctx context.Context, limit int) (int, error) {
	if s.transcoder == nil {
		return 0, ErrNoTranscoder
	}
	if !s.Enabled() {
		return 0, nil
	}
	rows, err := s.repo.ClaimDueTranscodeJobs(ctx, int32(limit))
	if err != nil {
		return 0, err
	}
	var (
		mu   sync.Mutex
		done int
	)
	workerpool.Run(s.Concurrency(), len(rows), func(i int) {
		row := rows[i]
		// The BOOKKEEPING writes (complete / reschedule / dead-letter) must
		// survive the very cancellation that ends the job. On SIGTERM ctx is
		// already cancelled by the time runTarget returns, so writing the outcome
		// through it fails silently — and the row that was just abandoned stays
		// 'running' forever, which the partial unique index then treats as a live
		// job (no re-enqueue, permanent 409 on admin re-transcode). Detaching
		// cancellation keeps the trace/correlation values while giving the write
		// its own bounded budget; the transcode work itself still honours ctx, so
		// this does not delay shutdown by more than that budget.
		bookkeeping, cancelBookkeeping := context.WithTimeout(
			context.WithoutCancel(ctx), bookkeepingTimeout)
		defer cancelBookkeeping()

		err := s.runTarget(ctx, row)
		if err != nil {
			s.recordFailure(bookkeeping, row, err)
			return
		}
		_ = s.repo.CompleteTranscodeJob(bookkeeping, row.ID)
		// Best-effort completion hook (IPFS mirror HLS-tree pin, P19.4). A hook
		// failure must never fail the job — the transcode already succeeded.
		if s.onComplete != nil {
			s.onComplete(ctx, row.VideoID)
		}
		mu.Lock()
		done++
		mu.Unlock()
	})
	return done, nil
}

func (s *Service) runTarget(ctx context.Context, row sqlcgen.ClaimDueTranscodeJobsRow) error {
	target := row.TranscodeType
	if target == "" {
		target = TargetAll
	}
	advanced, ok := s.transcoder.(TargetTranscoder)
	if !ok {
		res, err := s.transcoder.Transcode(ctx, row.VideoID, row.SourceKey)
		if err != nil {
			return err
		}
		return s.storeResult(ctx, row.VideoID, res)
	}
	progress := func(p media.TranscodeProgress) {
		repo, ok := s.repo.(stepRepository)
		if !ok {
			return
		}
		_ = repo.UpsertTranscodeStep(ctx, sqlcgen.UpsertTranscodeStepParams{
			TranscodeJobID:  row.ID,
			VideoID:         row.VideoID,
			Format:          p.Format,
			Height:          int32(p.Height),
			Width:           int32(p.Width),
			State:           p.State,
			Stage:           p.Stage,
			ProgressPercent: int16(p.Percent),
		})
	}
	// One probe per job, shared by every target below. A probe is a full source
	// read on a path-less backend, so this is the difference between one and two
	// extra downloads of the original per 'all' job.
	md, err := advanced.Probe(ctx, row.SourceKey)
	if err != nil {
		return err
	}
	if target == TargetAll || target == TargetHLS {
		res, err := advanced.TranscodeHLS(ctx, row.VideoID, row.SourceKey, md, progress)
		if err != nil {
			return &targetRunError{target: TargetHLS, err: err}
		}
		if err := s.storeResult(ctx, row.VideoID, res); err != nil {
			return &targetRunError{target: TargetHLS, err: err}
		}
	}
	if target == TargetAll || target == TargetWebVideo {
		files, err := advanced.TranscodeWebVideos(ctx, row.VideoID, row.SourceKey, md, progress)
		if err != nil {
			return &targetRunError{target: TargetWebVideo, err: err}
		}
		if err := s.storeWebVideos(ctx, row.VideoID, files); err != nil {
			return &targetRunError{target: TargetWebVideo, err: err}
		}
	}
	return nil
}

// storeResult persists a successful transcode: the video's renditions are
// replaced wholesale and its streaming playlist upserted as ready.
func (s *Service) storeResult(ctx context.Context, videoID uuid.UUID, res media.HLSResult) error {
	if err := s.repo.DeleteVideoRenditions(ctx, videoID); err != nil {
		return err
	}
	for _, r := range res.Renditions {
		if _, err := s.repo.CreateVideoRendition(ctx, sqlcgen.CreateVideoRenditionParams{
			VideoID:   videoID,
			Height:    int32(r.Height),
			Width:     int32(r.Width),
			KeyPrefix: r.KeyPrefix,
			SizeBytes: r.SizeBytes,
		}); err != nil {
			return err
		}
	}
	// Progressive VP9/WebM alternate (when TRANSCODING_VP9_ENABLED): recorded as a
	// single kind='webm' video_file, replacing any prior one, so /download can
	// surface it. Absent when VP9 is off.
	if res.WebMKey != "" {
		if err := s.repo.DeleteVideoFilesByVideoAndKind(ctx, sqlcgen.DeleteVideoFilesByVideoAndKindParams{
			VideoID: videoID,
			Kind:    "webm",
		}); err != nil {
			return err
		}
		if _, err := s.repo.CreateVideoFile(ctx, sqlcgen.CreateVideoFileParams{
			VideoID:      videoID,
			Kind:         "webm",
			StorageKey:   res.WebMKey,
			ContentType:  media.WebMContentType,
			OriginalName: "vp9.webm",
			SizeBytes:    res.WebMBytes,
		}); err != nil {
			return err
		}
	}
	_, err := s.repo.UpsertStreamingPlaylist(ctx, sqlcgen.UpsertStreamingPlaylistParams{
		VideoID:   videoID,
		MasterKey: res.MasterKey,
		State:     PlaylistReady,
	})
	return err
}

func (s *Service) storeWebVideos(ctx context.Context, videoID uuid.UUID, files []media.WebVideoResult) error {
	if err := s.repo.DeleteVideoFilesByVideoAndKind(ctx, sqlcgen.DeleteVideoFilesByVideoAndKindParams{
		VideoID: videoID,
		Kind:    "rendition",
	}); err != nil {
		return err
	}
	for _, f := range files {
		if _, err := s.repo.CreateVideoFile(ctx, sqlcgen.CreateVideoFileParams{
			VideoID:      videoID,
			Kind:         "rendition",
			StorageKey:   f.StorageKey,
			ContentType:  media.HLSMP4ContentType,
			OriginalName: fmt.Sprintf("%dp.mp4", f.Height),
			SizeBytes:    f.SizeBytes,
		}); err != nil {
			return err
		}
	}
	return nil
}

// recordFailure reschedules with backoff, or dead-letters after the cap (also
// marking the video's playlist failed so the outcome is observable).
//
// ctx here is the DETACHED bookkeeping context from DrainJobs, not the worker's:
// this runs precisely when the job was cancelled, so a cancellation-bound
// context would drop the reschedule write and strand the row in 'running'.
func (s *Service) recordFailure(ctx context.Context, row sqlcgen.ClaimDueTranscodeJobsRow, cause error) {
	attempts := int(row.Attempts) + 1
	msg := cause.Error()
	if len(msg) > maxLastErrorLen {
		msg = msg[:maxLastErrorLen]
	}
	if attempts >= maxAttempts {
		_ = s.repo.FailTranscodeJob(ctx, sqlcgen.FailTranscodeJobParams{ID: row.ID, LastError: msg})
		affectsHLS := row.TranscodeType != TargetWebVideo
		var targetErr *targetRunError
		if errors.As(cause, &targetErr) {
			affectsHLS = targetErr.target == TargetHLS
		}
		if affectsHLS {
			_, _ = s.repo.UpsertStreamingPlaylist(ctx, sqlcgen.UpsertStreamingPlaylistParams{
				VideoID:   row.VideoID,
				MasterKey: "",
				State:     PlaylistFailed,
			})
		}
		// Best-effort terminal-failure hook: release a publish-after-transcode
		// hold so the video publishes from its (playable) original rather than
		// staying hidden forever. Fired only on dead-letter, per completed video.
		if s.onFail != nil {
			s.onFail(ctx, row.VideoID)
		}
		return
	}
	_ = s.repo.RescheduleTranscodeJob(ctx, sqlcgen.RescheduleTranscodeJobParams{
		ID:            row.ID,
		NextAttemptAt: time.Now().UTC().Add(backoff(attempts)),
		LastError:     msg,
	})
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

// Playlist returns a video's streaming playlist row. The bool is false when no
// playlist has been recorded (not transcoded yet, or transcoding disabled) — a
// miss is reported as absent, not an error.
func (s *Service) Playlist(ctx context.Context, videoID uuid.UUID) (sqlcgen.StreamingPlaylist, bool) {
	sp, err := s.repo.GetStreamingPlaylist(ctx, videoID)
	if err != nil {
		return sqlcgen.StreamingPlaylist{}, false
	}
	return sp, true
}

// Renditions returns a video's stored HLS renditions, tallest first (empty when
// none).
func (s *Service) Renditions(ctx context.Context, videoID uuid.UUID) []sqlcgen.VideoRendition {
	rows, err := s.repo.ListVideoRenditions(ctx, videoID)
	if err != nil {
		return nil
	}
	return rows
}
