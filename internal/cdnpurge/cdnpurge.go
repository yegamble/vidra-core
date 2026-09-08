// Package cdnpurge is the durable half of the CDN invalidation seam: the queue
// (migration 0137) behind every edge purge that cannot finish inside the
// request that caused it.
//
// # What it is for
//
// internal/httpapi's media_purge.go fires an IMMEDIATE, detached fan-out at the
// moment an edge-cached object becomes wrong — a deletion, a privacy flip, a
// download gate closing. That is the right shape for the common case and stays
// exactly as it was. What it cannot do is anything that outlives one process:
//
//   - A REJECTED PURGE. Measured against a caching edge (docs/release-readiness.md,
//     "A32/A33 delivery"), eighteen purges rejected with 500 produced one warning
//     line and no second attempt; thirty seconds later the edge was still serving
//     the deleted video's poster. An edge answering 500 is an outage, not a
//     verdict, and the correct answer is the exponential backoff every other
//     queue in this schema already uses.
//   - AN ACCOUNT DELETION. One click cascades N channels' videos away. The URLs
//     have to be captured before the rows that name them disappear, and an api
//     restart between the capture and the purge must not lose them.
//   - THE INSTANCE-WIDE downloads_enabled TOGGLE. Closing it revokes several
//     URLs on EVERY public video at once — a walk over the whole catalogue, which
//     needs a lease and a resumable cursor rather than a detached goroutine that
//     a restart silently abandons half-way.
//
// # Why the paths live in the database
//
// media_purge.go refuses to LOG a media path, because the operator-supplied
// purge template can carry a credential in its query string and a log line must
// not become the place that leaks. Storing a path is a different question with a
// different answer: what is stored is this api's own ROUTE path
// (/api/v1/videos/<id>/thumbnail), every component of which is already a column
// of this database, and the DELIVERY_CDN_PURGE_* configuration never reaches
// this package at all — it stays behind the Purger seam, in internal/cdn.
//
// # Where it runs
//
// In whatever process runs background workers, which is why nothing here
// depends on internal/httpapi: cmd/api does not construct httpapi.Server at
// VIDRA_ROLE=worker. The media route grammar it needs is internal/mediaroute's,
// shared with the request handlers so a takedown and a viewer name the same URL.
package cdnpurge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/mediaroute"
	"github.com/vidra/vidra-core/internal/retry"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Reason labels why a job exists. A closed vocabulary this package writes —
// never free text from a request — because it reaches an operator's screen.
const (
	// ReasonRetry is a URL set an immediate pass could not invalidate.
	ReasonRetry = "retry"
	// ReasonAccountDelete is the snapshot taken before an account's cascade.
	ReasonAccountDelete = "account_delete"
	// ReasonDownloadsRevoked is the instance-wide downloads_enabled walk.
	ReasonDownloadsRevoked = "downloads_revoked"
)

// Job kinds, matching the CHECK constraint in 0137.
const (
	kindURLs             = "urls"
	kindDownloadsRevoked = "downloads_revoked"
)

// The retry schedule. base doubled per attempt already spent, capped — the
// arithmetic every queue-backed worker here shares (internal/retry).
//
// WHY THESE NUMBERS. The failure being scheduled against is a third-party edge
// that has stopped accepting invalidations, and the two ways to get it wrong are
// symmetric: retry too fast and a purge storm arrives at an API that is already
// rate-limiting, retry too slowly and wrong bytes stay served for a shift. One
// minute is far longer than any transient 5xx and far shorter than a CDN's own
// default TTL; one hour is the ceiling because an edge down for an hour is an
// incident an operator is already handling, and eight attempts spends
// 1+2+4+8+16+32+60 = 123 MINUTES of wall clock after the immediate pass before
// giving up. So the honest sentence for an operator is: a transient edge failure
// self-heals within about two hours, and anything that has not healed by then is
// on the dead-letter list with its URLs intact.
const (
	// DefaultRetryBase is the delay before the first persisted retry.
	DefaultRetryBase = time.Minute
	// DefaultRetryMax caps the doubling.
	DefaultRetryMax = time.Hour
	// DefaultMaxAttempts counts the IMMEDIATE pass as attempt 1, so this is
	// seven persisted retries.
	DefaultMaxAttempts = 8
	// DefaultLease is how long a claimed job is another worker's to finish. It
	// is sized for the catalogue walk's batch (hundreds of sequential
	// third-party calls), not for the one-URL retry.
	DefaultLease = 10 * time.Minute
	// DefaultWalkPage is how many videos one batch of the downloads-revocation
	// walk covers. Each video is up to a handful of purge calls, so a page is a
	// few hundred sequential HTTP requests — enough that the walk finishes a
	// large catalogue in minutes, small enough that a restart re-does at most
	// one page.
	DefaultWalkPage = 100
	// DefaultDoneTTL / DefaultFailedTTL are how long finished rows are kept. A
	// success is a fact about the past; a dead letter is a to-do list, and is
	// kept for a fortnight so a takedown that failed over a weekend is still
	// on screen on Monday.
	DefaultDoneTTL   = 7 * 24 * time.Hour
	DefaultFailedTTL = 14 * 24 * time.Hour
)

// maxJobPaths bounds one enqueued URL set. It matches media_purge.go's
// maxVideoPurgePaths for the same reason that cap exists — every path is a
// third-party HTTP call — and additionally keeps the jsonb column inside the
// size CHECK 0137 declares.
const maxJobPaths = 5000

// Purger invalidates ONE media route path at the edge. It is
// delivery.Resolver.Purge, handed in as a func so nothing here knows what a CDN
// is; a nil Purger means no CDN is configured, and NewService returns nil.
type Purger func(ctx context.Context, mediaPath string) error

// Recorder files one immediate pass's outcome with the in-process counters the
// admin status page reads (internal/httpapi's cdn_purge block). It is a func
// because those counters are process-wide state that belongs to the surface
// that renders them, and a worker-only process has no such surface — a nil
// Recorder is the correct wiring there, not a missing one.
type Recorder func(purged, failed int, complete bool)

// Repository is the data access this package needs. *sqlcgen.Queries satisfies
// it directly; tests substitute an in-memory fake.
type Repository interface {
	EnqueueCDNPurgeURLs(ctx context.Context, arg sqlcgen.EnqueueCDNPurgeURLsParams) (uuid.UUID, error)
	EnqueueCDNPurgeDownloadsRevoked(ctx context.Context) (uuid.UUID, error)
	ClaimDueCDNPurgeJobs(ctx context.Context, arg sqlcgen.ClaimDueCDNPurgeJobsParams) ([]sqlcgen.ClaimDueCDNPurgeJobsRow, error)
	CompleteCDNPurgeJob(ctx context.Context, arg sqlcgen.CompleteCDNPurgeJobParams) error
	AdvanceCDNPurgeWalk(ctx context.Context, arg sqlcgen.AdvanceCDNPurgeWalkParams) error
	RescheduleCDNPurgeJob(ctx context.Context, arg sqlcgen.RescheduleCDNPurgeJobParams) error
	FailCDNPurgeJob(ctx context.Context, arg sqlcgen.FailCDNPurgeJobParams) error
	DeleteFinishedCDNPurgeJobs(ctx context.Context, arg sqlcgen.DeleteFinishedCDNPurgeJobsParams) (int64, error)
	ListPublicDownloadableVideoIDs(ctx context.Context, arg sqlcgen.ListPublicDownloadableVideoIDsParams) ([]uuid.UUID, error)
	ListVideoDownloadFacts(ctx context.Context, videoID uuid.UUID) (sqlcgen.ListVideoDownloadFactsRow, error)
	CDNPurgeJobStats(ctx context.Context) (sqlcgen.CDNPurgeJobStatsRow, error)
}

// Service owns the queue.
type Service struct {
	repo   Repository
	purge  Purger
	record Recorder
	logger *slog.Logger
	now    func() time.Time

	retryBase   time.Duration
	retryMax    time.Duration
	maxAttempts int
	lease       time.Duration
	walkPage    int
	doneTTL     time.Duration
	failedTTL   time.Duration
}

// Option configures the service.
type Option func(*Service)

// WithLogger sets the logger. WithClock, WithRetry, WithLease and WithWalkPage
// exist for the tests, which cannot wait a minute to observe a backoff.
func WithLogger(l *slog.Logger) Option { return func(s *Service) { s.logger = l } }

// WithRecorder wires the in-process purge counters.
func WithRecorder(r Recorder) Option { return func(s *Service) { s.record = r } }

func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

func WithRetry(base, max time.Duration, maxAttempts int) Option {
	return func(s *Service) {
		if base > 0 {
			s.retryBase = base
		}
		if max > 0 {
			s.retryMax = max
		}
		if maxAttempts > 0 {
			s.maxAttempts = maxAttempts
		}
	}
}

func WithLease(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.lease = d
		}
	}
}

func WithWalkPage(n int) Option {
	return func(s *Service) {
		if n > 0 {
			s.walkPage = n
		}
	}
}

// NewService builds the queue service, or returns nil when there is nothing for
// it to do.
//
// A nil Purger is the "no CDN configured" case every install has by default,
// and returning nil rather than an inert service is the same free-by-default
// gate media_purge.go's cdnConfigured() applies: with no edge there is provably
// no shared copy, so a queue would spend a claim query every ten seconds to
// invalidate nothing. Every method below is nil-receiver safe, so callers do not
// branch.
func NewService(repo Repository, purge Purger, opts ...Option) *Service {
	if repo == nil || purge == nil {
		return nil
	}
	s := &Service{
		repo:        repo,
		purge:       purge,
		logger:      slog.Default(),
		now:         time.Now,
		retryBase:   DefaultRetryBase,
		retryMax:    DefaultRetryMax,
		maxAttempts: DefaultMaxAttempts,
		lease:       DefaultLease,
		walkPage:    DefaultWalkPage,
		doneTTL:     DefaultDoneTTL,
		failedTTL:   DefaultFailedTTL,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// EnqueueRetry persists a URL set an IMMEDIATE pass could not invalidate, so a
// later attempt finishes what it started.
//
// attemptsSpent is how many attempts the caller has already made — 1 from
// media_purge.go's immediate fan-out — so the first persisted retry lands one
// base delay from now rather than immediately, and the attempt budget is shared
// between the two halves rather than restarted here.
func (s *Service) EnqueueRetry(ctx context.Context, paths []string, complete bool, attemptsSpent int) {
	s.enqueueURLs(ctx, ReasonRetry, paths, complete, attemptsSpent)
}

// EnqueueSnapshot persists a URL set captured BEFORE a state change that no
// immediate pass will cover — today, an account deletion's cascade. It is due
// straight away: the next drain is its first attempt.
//
// It is deliberately NOT an immediate goroutine plus a retry row. The point of
// the queue for this family is that the intent survives the process, and a
// fan-out that starts in a request and is only persisted once it fails would
// lose exactly the case a restart mid-cascade produces.
func (s *Service) EnqueueSnapshot(ctx context.Context, reason string, paths []string, complete bool) {
	s.enqueueURLs(ctx, reason, paths, complete, 0)
}

func (s *Service) enqueueURLs(ctx context.Context, reason string, paths []string, complete bool, attemptsSpent int) {
	if s == nil {
		return
	}
	paths = dedupe(paths)
	if len(paths) == 0 {
		return
	}
	if len(paths) > maxJobPaths {
		// The set is capped rather than dropped, and the cap is REPORTED as
		// incompleteness: a truncated takedown that claimed to be whole is
		// worse than one that says which half it covered.
		paths = paths[:maxJobPaths]
		complete = false
	}
	blob, err := json.Marshal(paths)
	if err != nil {
		s.logger.WarnContext(ctx, "cdn purge could not be queued; the edge may still be serving these objects",
			"reason", reason, "urls", len(paths))
		return
	}
	due := s.now()
	if attemptsSpent > 0 {
		due = due.Add(retry.Backoff(attemptsSpent, s.retryBase, s.retryMax))
	}
	if _, err := s.repo.EnqueueCDNPurgeURLs(ctx, sqlcgen.EnqueueCDNPurgeURLsParams{
		Reason:         reason,
		Urls:           blob,
		UrlSetComplete: complete,
		Attempts:       int32(attemptsSpent),
		NextAttemptAt:  due,
	}); err != nil {
		// Never a failed request: the mutation that caused this has already
		// committed, and turning a successful deletion into a 5xx because a
		// third-party cache might be stale would be strictly worse than the
		// stale copy. No path in the log, as everywhere in the purge seam.
		s.logger.WarnContext(ctx, "cdn purge could not be queued; the edge may still be serving these objects",
			"reason", reason, "urls", len(paths))
	}
}

// EnqueueDownloadsRevoked starts the catalogue walk the instance-wide
// downloads_enabled toggle needs when it closes.
//
// A second flip while a walk is still in flight adds nothing (ON CONFLICT DO
// NOTHING against 0137's partial unique index): the walk already covers the
// whole catalogue, and a duplicate would only double the third-party call
// volume for the same result.
func (s *Service) EnqueueDownloadsRevoked(ctx context.Context) {
	if s == nil {
		return
	}
	// pgx.ErrNoRows is the ON CONFLICT DO NOTHING no-op — "a walk is already
	// queued" — which is the success case, not a failure.
	if _, err := s.repo.EnqueueCDNPurgeDownloadsRevoked(ctx); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.logger.WarnContext(ctx, "cdn purge walk could not be queued; the edge may still be serving downloads that are now revoked")
	}
}

// DrainOnce claims up to limit due jobs and runs each one, returning how many it
// finished (done, dead-lettered, or advanced by one batch).
//
// Per-job failures are recorded IN THE QUEUE — rescheduled with backoff or
// dead-lettered — so the only error this returns is the claim query's.
func (s *Service) DrainOnce(ctx context.Context, limit int) (int, error) {
	if s == nil {
		return 0, nil
	}
	if limit <= 0 {
		limit = 1
	}
	rows, err := s.repo.ClaimDueCDNPurgeJobs(ctx, sqlcgen.ClaimDueCDNPurgeJobsParams{
		LeaseSeconds: s.lease.Seconds(),
		ClaimLimit:   int32(limit),
	})
	if err != nil {
		return 0, fmt.Errorf("cdnpurge: claim: %w", err)
	}
	handled := 0
	for _, row := range rows {
		if row.Kind == kindDownloadsRevoked {
			s.runWalkBatch(ctx, row)
		} else {
			s.runURLJob(ctx, row)
		}
		handled++
	}
	return handled, nil
}

// runURLJob issues one attempt at a fixed URL set.
//
// The set is REWRITTEN to what is still unpurged, which is what makes a retry
// cheap and correct: the edge never sees a second call for a URL it already
// accepted, and the row's own `urls` column doubles as the record of exactly
// what may still be stale.
func (s *Service) runURLJob(ctx context.Context, row sqlcgen.ClaimDueCDNPurgeJobsRow) {
	paths := decodePaths(row.Urls)
	remaining, purged, lastErr := s.purgeAll(ctx, paths)
	total := row.Purged + int32(purged)

	if len(remaining) == 0 {
		if err := s.repo.CompleteCDNPurgeJob(ctx, sqlcgen.CompleteCDNPurgeJobParams{ID: row.ID, Purged: total}); err != nil {
			s.logger.WarnContext(ctx, "cdn purge job completed but could not be recorded", "job_id", row.ID.String())
		}
		if !row.UrlSetComplete {
			// Every URL this job could NAME is gone from the edge, and the job
			// still leaves something behind. Saying so is the difference between
			// "the takedown is finished" and "the takedown is as finished as it
			// can be" — see expandEdgePurgePaths for what cannot be named.
			s.logger.WarnContext(ctx, "cdn purge finished from an incomplete URL set; the edge may still hold objects this instance cannot name",
				"job_id", row.ID.String(), "reason", row.Reason, "purged", total)
		}
		return
	}

	blob, err := json.Marshal(remaining)
	if err != nil {
		blob = []byte("[]")
	}
	spent := row.Attempts + 1
	summary := purgeErrorSummary(lastErr, len(remaining))
	if int(spent) >= s.maxAttempts {
		if err := s.repo.FailCDNPurgeJob(ctx, sqlcgen.FailCDNPurgeJobParams{
			ID: row.ID, Urls: blob, Purged: total, LastError: summary,
		}); err != nil {
			s.logger.WarnContext(ctx, "cdn purge dead letter could not be recorded", "job_id", row.ID.String())
			return
		}
		// The ONE line an operator has to act on: after this the edge keeps the
		// objects until its own TTL, and nothing else will try again.
		s.logger.ErrorContext(ctx, "cdn purge gave up after the attempt cap; the edge is still serving these objects and must be invalidated by hand",
			"job_id", row.ID.String(),
			"reason", row.Reason,
			"attempts", spent,
			"purged", total,
			"unpurged", len(remaining),
			"cause", summary)
		return
	}
	next := s.now().Add(retry.Backoff(int(spent), s.retryBase, s.retryMax))
	if err := s.repo.RescheduleCDNPurgeJob(ctx, sqlcgen.RescheduleCDNPurgeJobParams{
		ID: row.ID, Urls: blob, Purged: total, NextAttemptAt: next, LastError: summary,
	}); err != nil {
		s.logger.WarnContext(ctx, "cdn purge retry could not be scheduled", "job_id", row.ID.String())
	}
}

// runWalkBatch advances the downloads-revocation walk by one page.
//
// THE CURSOR ADVANCES EVEN WHEN A PURGE IN THE BATCH FAILED, and the failures
// are re-queued as their own retry job. A walk is not a transaction: a single
// video whose purge the edge keeps rejecting must not hold the other thirteen
// thousand hostage, and a retry ladder per URL set is exactly the machinery that
// already exists. What the walk row keeps is url_set_complete, so "the walk
// finished" and "the walk finished cleanly" stay different answers.
func (s *Service) runWalkBatch(ctx context.Context, row sqlcgen.ClaimDueCDNPurgeJobsRow) {
	after := uuid.Nil
	if row.CursorVideoID.Valid {
		after = uuid.UUID(row.CursorVideoID.Bytes)
	}
	ids, err := s.repo.ListPublicDownloadableVideoIDs(ctx, sqlcgen.ListPublicDownloadableVideoIDsParams{
		After:     after,
		PageLimit: int32(s.walkPage),
	})
	if err != nil {
		// A read failure is not a purge failure: reschedule with backoff and
		// keep the cursor, so the walk resumes where it was.
		s.rescheduleWalk(ctx, row, "the catalogue could not be read")
		return
	}
	if len(ids) == 0 {
		if cerr := s.repo.CompleteCDNPurgeJob(ctx, sqlcgen.CompleteCDNPurgeJobParams{ID: row.ID, Purged: row.Purged}); cerr != nil {
			s.logger.WarnContext(ctx, "cdn purge walk completed but could not be recorded", "job_id", row.ID.String())
			return
		}
		s.logger.InfoContext(ctx, "cdn purge walk finished revoking downloads at the edge",
			"job_id", row.ID.String(), "purged", row.Purged, "url_set_complete", row.UrlSetComplete)
		return
	}

	var paths []string
	batchComplete := true
	for _, id := range ids {
		facts, ferr := s.repo.ListVideoDownloadFacts(ctx, id)
		if ferr != nil {
			// One unreadable video is recorded as incompleteness rather than
			// aborting the page: the rest of the batch is still invalidated.
			batchComplete = false
			continue
		}
		paths = append(paths, downloadPaths(id, facts)...)
	}
	remaining, purged, lastErr := s.purgeAll(ctx, paths)
	if len(remaining) > 0 {
		batchComplete = false
		// Hand the failures the standard retry ladder as their own job. They
		// start at attemptsSpent=1 because this batch WAS an attempt.
		s.EnqueueRetry(ctx, remaining, true, 1)
		s.logger.WarnContext(ctx, "cdn purge walk batch had rejections; they were queued for retry",
			"job_id", row.ID.String(), "unpurged", len(remaining), "cause", purgeErrorSummary(lastErr, len(remaining)))
	}
	if err := s.repo.AdvanceCDNPurgeWalk(ctx, sqlcgen.AdvanceCDNPurgeWalkParams{
		ID:            row.ID,
		CursorVideoID: pgtype.UUID{Bytes: ids[len(ids)-1], Valid: true},
		Purged:        row.Purged + int32(purged),
		BatchComplete: batchComplete,
	}); err != nil {
		s.logger.WarnContext(ctx, "cdn purge walk progress could not be recorded; the batch will be repeated", "job_id", row.ID.String())
	}
}

func (s *Service) rescheduleWalk(ctx context.Context, row sqlcgen.ClaimDueCDNPurgeJobsRow, why string) {
	spent := row.Attempts + 1
	if int(spent) >= s.maxAttempts {
		if err := s.repo.FailCDNPurgeJob(ctx, sqlcgen.FailCDNPurgeJobParams{
			ID: row.ID, Urls: []byte("[]"), Purged: row.Purged, LastError: why,
		}); err == nil {
			s.logger.ErrorContext(ctx, "cdn purge walk gave up; downloads may still be served from the edge for videos it did not reach",
				"job_id", row.ID.String(), "attempts", spent, "purged", row.Purged, "cause", why)
		}
		return
	}
	next := s.now().Add(retry.Backoff(int(spent), s.retryBase, s.retryMax))
	if err := s.repo.RescheduleCDNPurgeJob(ctx, sqlcgen.RescheduleCDNPurgeJobParams{
		ID: row.ID, Urls: []byte("[]"), Purged: row.Purged, NextAttemptAt: next, LastError: why,
	}); err != nil {
		s.logger.WarnContext(ctx, "cdn purge walk retry could not be scheduled", "job_id", row.ID.String())
	}
}

// PurgeReplacedAsset invalidates the ONE URL a video's poster or storyboard
// sprite is served at, after it was replaced in place.
//
// Both are the awkward case the rest of the purge seam does not have: their URL
// does not change when their bytes do, so a shared cache keeps answering with
// the old image. Everything else in a video's media set is either
// generation-addressed (the ladder, since migration 0136) or removed outright.
//
// kind is a video_files kind. Anything other than "storyboard" is the poster,
// because those are the only two kinds video.Service's replacement hook fires
// for and a silent no-op on an unexpected third would be a purge that did not
// happen.
func (s *Service) PurgeReplacedAsset(ctx context.Context, videoID uuid.UUID, kind string) {
	s.PurgeDetached(ctx, []string{ReplacedAssetPath(videoID, kind)}, true)
}

// ReplacedAssetPath is the media route URL of a replaced-in-place asset. It uses
// the same builders the request handlers do (internal/mediaroute) because an
// invalidation for a URL the routes do not serve would answer 404 at the edge,
// which internal/cdn counts as success — a takedown that reads as working in the
// log while the stale copy stays exactly where it is.
func ReplacedAssetPath(videoID uuid.UUID, kind string) string {
	if kind == "storyboard" {
		return mediaroute.Video(videoID, "/storyboard.jpg")
	}
	return mediaroute.Video(videoID, "/thumbnail")
}

// PurgeDetached invalidates a small, already-known URL set immediately and
// persists whatever the edge refused, so the retry ladder finishes the job.
//
// It is the single-shot sibling of the queue: the caller is a hook fired inside
// a request (a poster or storyboard replacement), so the work is detached with
// context.WithoutCancel for the same reason media_purge.go detaches its fan-out
// — a purge is a fan-out of third-party HTTP calls, and holding an upload open
// for a slow edge would make the edge look like a broken API.
func (s *Service) PurgeDetached(ctx context.Context, paths []string, complete bool) {
	if s == nil || len(paths) == 0 {
		return
	}
	ctx = context.WithoutCancel(ctx)
	go s.PurgeNow(ctx, paths, complete)
}

// PurgeNow is PurgeDetached's synchronous body: one immediate pass, the
// counters, and a persisted retry for whatever was refused. It returns how many
// URLs the edge accepted.
func (s *Service) PurgeNow(ctx context.Context, paths []string, complete bool) int {
	if s == nil {
		return 0
	}
	paths = dedupe(paths)
	if len(paths) == 0 {
		return 0
	}
	remaining, purged, _ := s.purgeAll(ctx, paths)
	if s.record != nil {
		s.record(purged, len(remaining), complete)
	}
	if len(remaining) > 0 {
		// attemptsSpent=1: this pass WAS the first attempt, so the ladder
		// continues rather than restarting.
		s.EnqueueRetry(ctx, remaining, complete, 1)
	}
	return purged
}

// purgeAll issues one purge per path and reports what did NOT land.
//
// Sequential and never short-circuiting, exactly like the immediate fan-out:
// purge APIs are rate-limited, a takedown is not latency-critical, and one
// object saying no tells you nothing about the next one.
func (s *Service) purgeAll(ctx context.Context, paths []string) (remaining []string, purged int, lastErr error) {
	for _, p := range paths {
		if err := s.purge(ctx, p); err != nil {
			remaining = append(remaining, p)
			lastErr = err
			continue
		}
		purged++
	}
	return remaining, purged, lastErr
}

// SweepFinished removes finished rows past their retention. Dead letters outlive
// successes because they are the ones that still need an operator.
func (s *Service) SweepFinished(ctx context.Context) (int, error) {
	if s == nil {
		return 0, nil
	}
	n, err := s.repo.DeleteFinishedCDNPurgeJobs(ctx, sqlcgen.DeleteFinishedCDNPurgeJobsParams{
		DoneTtlSeconds:   s.doneTTL.Seconds(),
		FailedTtlSeconds: s.failedTTL.Seconds(),
	})
	if err != nil {
		return 0, fmt.Errorf("cdnpurge: sweep: %w", err)
	}
	return int(n), nil
}

// Stats is the queue's operator-facing depth, read by GET /admin/system's
// cdn_purge block and by `vidra doctor`.
type Stats struct {
	// Pending is work still to do — invalidations the edge has not accepted.
	Pending int64
	// Running is claimed work. A walk spends most of its life here.
	Running int64
	// DeadLettered is jobs past the attempt cap. Each one is an edge still
	// serving something this instance has stopped serving.
	DeadLettered int64
	// OldestPendingAge is how long the oldest unfinished job has been waiting.
	// It is the staleness signal: a growing age means the edge is refusing.
	OldestPendingAge time.Duration
}

// Stats reads the queue depth. It returns a zero value and no error on a nil
// service, so the caller does not branch on "no CDN".
func (s *Service) Stats(ctx context.Context) (Stats, error) {
	if s == nil {
		return Stats{}, nil
	}
	row, err := s.repo.CDNPurgeJobStats(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("cdnpurge: stats: %w", err)
	}
	return Stats{
		Pending:          row.Pending,
		Running:          row.Running,
		DeadLettered:     row.Failed,
		OldestPendingAge: time.Duration(row.OldestPendingAgeSeconds) * time.Second,
	}, nil
}

// downloadPaths names every official-download URL one video exposes.
//
// It is the queue's copy of the question videoDownloadPurgeSnapshot asks per
// request, answered from one row instead of three service calls — and it uses
// the SAME path builders, which is the property that matters: an invalidation
// that named a URL the routes do not serve would answer 404 at the edge, which
// internal/cdn counts as success, and read as a working takedown in the log
// while the stale copy sat exactly where it was.
func downloadPaths(videoID uuid.UUID, f sqlcgen.ListVideoDownloadFactsRow) []string {
	var out []string
	if f.HasOriginal {
		out = append(out, mediaroute.Video(videoID, "/download/original"))
	}
	if f.HasWebm {
		out = append(out, mediaroute.Video(videoID, "/download/webm"))
	}
	if !f.HasPlaylist {
		// The derived download routes resolve through the finalized ladder, so
		// with no master key they 404 and the edge can hold nothing for them.
		return out
	}
	out = append(out, mediaroute.Video(videoID, "/download/audio"))
	for _, h := range f.RenditionHeights {
		if h <= 0 {
			continue
		}
		out = append(out,
			mediaroute.RenditionDownload(videoID, int(h), true),
			mediaroute.RenditionDownload(videoID, int(h), false))
	}
	return out
}

// decodePaths reads the jsonb URL list. A row this package cannot parse is
// treated as empty, which completes the job rather than retrying it forever:
// there is nothing left it could invalidate.
func decodePaths(blob []byte) []string {
	if len(blob) == 0 {
		return nil
	}
	var paths []string
	if err := json.Unmarshal(blob, &paths); err != nil {
		return nil
	}
	return dedupe(paths)
}

// dedupe drops empties and repeats, preserving order. One object is reachable at
// more than one route but a route is one edge entry, so a repeated path is a
// wasted third-party call.
func dedupe(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// purgeErrorSummary turns the last rejection into the one sentence the queue
// stores and shows.
//
// internal/cdn has already stripped the request URL and the provider's response
// body out of its errors, so what arrives is a status or a transport cause —
// which is what an operator can act on anyway. The count is carried alongside
// because "3 of 18 URLs" and "18 of 18" are different incidents.
func purgeErrorSummary(err error, unpurged int) string {
	if err == nil {
		return fmt.Sprintf("%d urls were not invalidated", unpurged)
	}
	return fmt.Sprintf("%d urls were not invalidated: %s", unpurged, err.Error())
}
