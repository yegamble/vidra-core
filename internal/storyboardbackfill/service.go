// Package storyboardbackfill generates the seek-preview storyboards that were
// never made, for videos that already live on this instance.
//
// Storyboards have only ever been produced on the two publish seams — a video
// finishing video.Service.Process, and ReplaceSource swapping in a new original
// — and both are best-effort and both are skipped outright when ffmpeg is absent
// or the storyboards_enabled overlay is off. Nothing has ever retried them. A
// video published on an afternoon when ffmpeg was not installed has no seek
// preview today and, before this package, would never have got one.
//
// A PeerTube migration opens the same hole much wider from the other side. The
// importer carries the source's own sprite sheet where there is one, which is
// exact and cheap, but PeerTube only generates storyboards from 6.0 onward, its
// generation job returns early for media under three seconds, and a source old
// enough to have no storyboard table yields nothing at all. Once the source
// instance is switched off, no amount of fetching brings the rest across. This
// is the durable fallback for all of it: whatever the import could not carry,
// Vidra renders for itself out of media it already holds.
//
// It is deliberately NOT a second implementation of generation. The rendering,
// the two blob writes and the delete-then-insert of the two video_files rows all
// stay in video.Service.GenerateStoryboard, which the publish paths call too;
// this package decides only WHICH videos to hand it and WHAT to remember about
// the ones that fail.
//
// Deliberately out of scope: thumbnails. Every video that has ever been
// published or imported already has a poster (PeerTube writes one for every
// video, and Process generates one), so there is no comparable backlog to walk.
package storyboardbackfill

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Retry policy.
//
// The unit of work here is a FULL DECODE of a video original — the sheet samples
// one frame per interval but ffmpeg still walks the file to find them — so the
// cost of being wrong about a retry is measured in CPU-minutes, not in a wasted
// SELECT. That is the same lesson the actor-image import learned the hard way,
// where five oversized rows were re-fetched from a live production instance on
// every single run until its outcomes were split into terminal and retryable
// ones.
//
// So: retry, because a failure really can be transient (the object store was
// unreachable, the box was out of temp space, ffmpeg was killed by an OOM under
// a concurrent transcode) — but a BOUNDED number of times, and then never again.
const (
	// MaxAttempts is how many failed generations a video gets before it is given
	// up on for good. Five, because the failures worth retrying are outages, and
	// an outage that has survived the whole backoff schedule below is not an
	// outage any more.
	MaxAttempts = 5

	// backoffBase is the wait after the FIRST failure; each further failure
	// doubles it, up to backoffMax. Hours rather than minutes because the retry
	// exists for outages, and re-decoding a failing video every few minutes is
	// precisely the self-inflicted load this package is written to avoid.
	backoffBase = 6 * time.Hour
	// backoffMax caps the doubling so a video does not disappear for a week
	// before its last chance. With these three numbers the schedule is
	// 6h, 12h, 24h, 24h — a video that is going to be given up on is given up on
	// about three days after its first failure, having cost five decodes total.
	backoffMax = 24 * time.Hour
)

// Repository is the data access the backfill needs. *sqlcgen.Queries satisfies
// it directly; tests substitute an in-memory fake.
type Repository interface {
	ListVideosNeedingStoryboard(ctx context.Context, limit int32) ([]sqlcgen.ListVideosNeedingStoryboardRow, error)
	RecordStoryboardAttemptFailure(ctx context.Context, arg sqlcgen.RecordStoryboardAttemptFailureParams) (sqlcgen.RecordStoryboardAttemptFailureRow, error)
	GiveUpOnStoryboard(ctx context.Context, arg sqlcgen.GiveUpOnStoryboardParams) error
	ClearStoryboardAttempt(ctx context.Context, videoID uuid.UUID) error
}

// Generator renders and stores a video's storyboard pair, reporting why it could
// not. *video.Service satisfies it via GenerateStoryboard; the interface is here
// so this package can be tested without ffmpeg, a blob store or a database.
type Generator interface {
	GenerateStoryboard(ctx context.Context, videoID uuid.UUID, originalKey string, durationHint int) error
}

// Service walks the storyboard-less part of the catalogue and fills it in.
type Service struct {
	repo   Repository
	gen    Generator
	logger *slog.Logger
	now    func() time.Time
}

// NewService builds the backfill service. A nil logger falls back to the default
// one.
func NewService(repo Repository, gen Generator, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, gen: gen, logger: logger, now: time.Now}
}

// Result is one pass's outcome, sized for a single honest log line. Scanned == 0
// means nothing was DUE — which is not quite the same as finished, because a
// video waiting out a backoff is not scanned either.
type Result struct {
	// Scanned is how many eligible videos the pass picked up.
	Scanned int
	// Generated is how many now have a storyboard.
	Generated int
	// Retrying is how many failed and are parked behind a backoff for another go.
	Retrying int
	// GaveUp is how many were booked as permanently unfixable in this pass —
	// either by exhausting MaxAttempts or, straight away, because the source has
	// no measurable duration.
	GaveUp int
}

// BackfillOnce generates storyboards for up to batch eligible videos, oldest
// first, and returns what it did. It is the unit the worker ticks.
//
// Work is strictly SEQUENTIAL, and that is load-bearing rather than incidental:
// each item is a full ffmpeg decode, so however large batch is, at most one
// decode is ever in flight. The batch size and the worker's interval set how
// much of the wall clock this occupies; they cannot make it occupy more than one
// process.
//
// A per-video failure is booked in the ledger and the pass continues: one
// undecodable original must not stall the rest of the catalogue. Only a database
// error, which makes the whole pass meaningless, is returned.
//
// Callers must run this on ONE instance at a time (the worker is leader-gated).
// It claims nothing, so two concurrent passes would decode the same videos
// twice; the writes would still be correct, just wasted — and wasted here is
// expensive.
func (s *Service) BackfillOnce(ctx context.Context, batch int32) (Result, error) {
	var res Result
	if s.gen == nil {
		return res, errors.New("storyboardbackfill: no storyboard generator configured")
	}
	rows, err := s.repo.ListVideosNeedingStoryboard(ctx, batch)
	if err != nil {
		return res, err
	}
	res.Scanned = len(rows)
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		gerr := s.gen.GenerateStoryboard(ctx, row.ID, row.StorageKey, int(row.DurationSeconds))
		if gerr == nil {
			if cerr := s.repo.ClearStoryboardAttempt(ctx, row.ID); cerr != nil {
				return res, cerr
			}
			res.Generated++
			continue
		}
		// A context cancellation is the worker shutting down, not the video's
		// fault. Booking a failure against it would spend an attempt on a video
		// nothing was actually wrong with, so the pass just stops.
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		terminal, err := s.recordFailure(ctx, row.ID, int(row.Attempts), gerr)
		if err != nil {
			return res, err
		}
		if terminal {
			res.GaveUp++
		} else {
			res.Retrying++
		}
	}
	return res, nil
}

// recordFailure books one failed generation and reports whether the video has
// now been given up on permanently. priorAttempts is how many times it had
// already failed before this one, which is what sizes the next backoff; the scan
// returns it so this costs no extra round-trip.
//
// The give-up is logged HERE, once, at the moment it is decided — not by the
// worker's per-tick summary, which only carries counts, and not on later ticks,
// because for this video there are none. This line is the whole record an
// operator has of which videos will never get a seek preview and why, so it
// carries the reason verbatim.
func (s *Service) recordFailure(ctx context.Context, videoID uuid.UUID, priorAttempts int, gerr error) (terminal bool, err error) {
	// The one provably permanent failure. PlanStoryboard computes the sheet from
	// the duration and nothing else, so a source that yields no duration — not
	// from the caller's hint, not from a probe — can never produce a plan, and
	// re-running is re-running the same probe for the same nothing. It skips the
	// retry budget entirely rather than burning four more full decodes on it.
	if errors.Is(gerr, media.ErrNoMeasurableDuration) {
		if err := s.repo.GiveUpOnStoryboard(ctx, sqlcgen.GiveUpOnStoryboardParams{
			VideoID: videoID, LastError: gerr.Error(),
		}); err != nil {
			return false, err
		}
		s.logger.WarnContext(ctx, "storyboard backfill gave up on a video: its source has no measurable duration, so no sprite sheet can ever be laid out",
			"video_id", videoID.String(), "error", gerr.Error())
		return true, nil
	}
	out, err := s.repo.RecordStoryboardAttemptFailure(ctx, sqlcgen.RecordStoryboardAttemptFailureParams{
		VideoID:       videoID,
		NextAttemptAt: s.now().Add(backoffFor(priorAttempts)),
		LastError:     gerr.Error(),
		MaxAttempts:   MaxAttempts,
	})
	if err != nil {
		return false, err
	}
	if out.GivenUp {
		s.logger.WarnContext(ctx, "storyboard backfill gave up on a video after exhausting its retries; it will never have a seek preview",
			"video_id", videoID.String(), "attempts", out.Attempts, "error", gerr.Error())
		return true, nil
	}
	s.logger.WarnContext(ctx, "storyboard backfill could not generate a storyboard; it will be retried",
		"video_id", videoID.String(), "attempts", out.Attempts, "error", gerr.Error())
	return false, nil
}

// backoffFor is how long a video waits after its priorAttempts-th failure:
// backoffBase doubled once per prior failure, capped at backoffMax.
func backoffFor(priorAttempts int) time.Duration {
	d := backoffBase
	for i := 0; i < priorAttempts; i++ {
		d *= 2
		if d >= backoffMax {
			return backoffMax
		}
	}
	return d
}
