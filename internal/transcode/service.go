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
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
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
	UpsertStreamingPlaylist(ctx context.Context, arg sqlcgen.UpsertStreamingPlaylistParams) (sqlcgen.StreamingPlaylist, error)
	GetStreamingPlaylist(ctx context.Context, videoID uuid.UUID) (sqlcgen.StreamingPlaylist, error)
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
// a no-op (partial unique index + ON CONFLICT DO NOTHING).
func (s *Service) Enqueue(ctx context.Context, videoID uuid.UUID, sourceKey string) error {
	return s.repo.EnqueueTranscodeJob(ctx, sqlcgen.EnqueueTranscodeJobParams{
		VideoID:   videoID,
		SourceKey: sourceKey,
	})
}

// DrainJobs claims up to limit due jobs and runs each through the transcoder:
// on success the video's renditions are replaced, its streaming playlist is
// marked ready, and the job completes; on failure the job is rescheduled with
// exponential backoff, or dead-lettered (and the playlist marked failed) after
// maxAttempts. Returns the number completed. Only the claim-query error is
// returned — per-job failures are persisted in the queue, not surfaced.
// Intended to be called on a ticker by a single worker.
func (s *Service) DrainJobs(ctx context.Context, limit int) (int, error) {
	if s.transcoder == nil {
		return 0, ErrNoTranscoder
	}
	rows, err := s.repo.ClaimDueTranscodeJobs(ctx, int32(limit))
	if err != nil {
		return 0, err
	}
	done := 0
	for _, row := range rows {
		res, err := s.transcoder.Transcode(ctx, row.VideoID, row.SourceKey)
		if err == nil {
			err = s.storeResult(ctx, row.VideoID, res)
		}
		if err != nil {
			s.recordFailure(ctx, row, err)
			continue
		}
		_ = s.repo.CompleteTranscodeJob(ctx, row.ID)
		// Best-effort completion hook (IPFS mirror HLS-tree pin, P19.4). A hook
		// failure must never fail the job — the transcode already succeeded.
		if s.onComplete != nil {
			s.onComplete(ctx, row.VideoID)
		}
		done++
	}
	return done, nil
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

// recordFailure reschedules with backoff, or dead-letters after the cap (also
// marking the video's playlist failed so the outcome is observable).
func (s *Service) recordFailure(ctx context.Context, row sqlcgen.ClaimDueTranscodeJobsRow, cause error) {
	attempts := int(row.Attempts) + 1
	msg := cause.Error()
	if len(msg) > maxLastErrorLen {
		msg = msg[:maxLastErrorLen]
	}
	if attempts >= maxAttempts {
		_ = s.repo.FailTranscodeJob(ctx, sqlcgen.FailTranscodeJobParams{ID: row.ID, LastError: msg})
		_, _ = s.repo.UpsertStreamingPlaylist(ctx, sqlcgen.UpsertStreamingPlaylistParams{
			VideoID:   row.VideoID,
			MasterKey: "",
			State:     PlaylistFailed,
		})
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
