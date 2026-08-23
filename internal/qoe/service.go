package qoe

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Repository is the data access this package needs. *sqlcgen.Queries satisfies
// it; tests substitute a fake, which is what lets the rollup arithmetic be
// proven by `make ci` on a runner with no database.
type Repository interface {
	InsertQoEEvent(ctx context.Context, arg sqlcgen.InsertQoEEventParams) error
	LatestQoERollupHour(ctx context.Context) ([]time.Time, error)
	ListQoERollupBuckets(ctx context.Context, arg sqlcgen.ListQoERollupBucketsParams) ([]time.Time, error)
	ListQoEEventsForBucketPage(ctx context.Context, arg sqlcgen.ListQoEEventsForBucketPageParams) ([]sqlcgen.ListQoEEventsForBucketPageRow, error)
	UpsertQoERollup(ctx context.Context, arg sqlcgen.UpsertQoERollupParams) error
	ListQoERollups(ctx context.Context, arg sqlcgen.ListQoERollupsParams) ([]sqlcgen.QoeRollup, error)
	CountQoERollups(ctx context.Context, arg sqlcgen.CountQoERollupsParams) (int64, error)
	PruneQoEEvents(ctx context.Context, arg sqlcgen.PruneQoEEventsParams) (int64, error)
	PruneQoERollups(ctx context.Context, arg sqlcgen.PruneQoERollupsParams) (int64, error)
}

// Metrics receives best-effort drop counts (nil is fine). Implemented by
// observability.Metrics.
//
// The label is an EventType — a closed, four-member vocabulary — and never a
// video id, a source host or an error message. QoE is an event/rollup stream,
// never Prometheus labels; the one counter that does exist here counts the
// events this package failed to store, which is a property of the pipeline
// rather than of the playback.
type Metrics interface {
	IncQoEDrop(eventType string)
}

// Retention windows. Fixed, like jobstatus's: an operator-tunable retention is a
// knob whose wrong setting is discovered months later, when the data that would
// have answered the question is gone.
const (
	// RawRetention is how long individual measurements survive. Short on
	// purpose — this table grows with viewer-seconds, and the rollups are what
	// the admin view reads.
	RawRetention = 7 * 24 * time.Hour
	// RollupRetention matches jobstatus's terminal-run window, so an operator
	// has one number in their head for "how far back does Vidra remember".
	RollupRetention = 90 * 24 * time.Hour

	// pruneBatchSize matches jobstatus.Prune. Batching bounds the lock
	// footprint of a delete that could otherwise touch millions of rows.
	pruneBatchSize = int32(10000)
	// pruneMaxBatches caps one sweep at 1M rows per table. Unlike jobstatus,
	// which prunes a queue that grows with operator activity, this table grows
	// with traffic — a single-batch sweep on an hourly tick would fall behind an
	// instance doing more than 10k events an hour and never catch up. The cap is
	// what keeps a catch-up sweep from becoming an unbounded transaction storm.
	pruneMaxBatches = 100

	// rollupPageSize is one keyset page of raw events during a rollup.
	rollupPageSize = int32(5000)
	// maxBucketsPerSweep caps how many hours one rollup tick will process, so
	// an instance coming back from a long outage catches up over several ticks
	// instead of in one very long one.
	maxBucketsPerSweep = int32(48)
	// bucketGrace is how long after an hour ends before it is rolled up. It
	// covers beacons already in flight at the boundary; because received_at is
	// the SERVER's clock, nothing can arrive for an hour more than one request
	// round trip after it closed, so this is generous rather than load-bearing.
	bucketGrace = 5 * time.Minute
)

// ErrUnavailable is returned when the service has no repository wired.
var ErrUnavailable = errors.New("qoe: repository unavailable")

// Service records measurements, rolls them up, and enforces retention.
//
// A nil *Service is a valid receiver for Record, so call sites can be wired
// unconditionally exactly as searchevents.Enqueuer is.
type Service struct {
	repo    Repository
	logger  *slog.Logger
	metrics Metrics
}

// Option customises the Service.
type Option func(*Service)

// WithMetrics wires the drop counter.
func WithMetrics(m Metrics) Option { return func(s *Service) { s.metrics = m } }

// NewService builds a Service over repo. logger defaults to slog.Default().
func NewService(repo Repository, logger *slog.Logger, opts ...Option) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Service{repo: repo, logger: logger}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Record persists one validated measurement. It is BEST EFFORT in the strongest
// sense: a write failure is logged, metered and swallowed, and the caller is
// told nothing. That is the same contract searchevents.Enqueuer has, and it is
// the right one — a viewer's playback must never fail, slow down, or surface an
// error because a telemetry row could not be written.
//
// The event must already have passed Validate; Record does not re-check the
// vocabularies, because a caller that skipped validation would be a programming
// error the schema's CHECK constraints will catch loudly in tests.
func (s *Service) Record(ctx context.Context, e Event) {
	if s == nil || s.repo == nil {
		return
	}
	var errClass *string
	if e.ErrorClass != nil {
		v := string(*e.ErrorClass)
		errClass = &v
	}
	err := s.repo.InsertQoEEvent(ctx, sqlcgen.InsertQoEEventParams{
		EventType:       string(e.Type),
		DeliverySource:  string(e.DeliverySource),
		Engine:          string(e.Engine),
		PackagingFormat: string(e.PackagingFormat),
		VideoID:         optionalUUID(e.VideoID),
		LiveStreamID:    optionalUUID(e.LiveStreamID),
		SessionID:       optionalUUID(e.SessionID),
		SessionVerified: e.SessionVerified,
		ViewerDigest:    e.ViewerDigest,
		TtffMs:          e.TTFFMs,
		RebufferMs:      e.RebufferMs,
		RenditionHeight: e.RenditionHeight,
		ErrorClass:      errClass,
		Metadata:        SafeMetadata(e.Metadata),
	})
	if err != nil {
		// The error may name a column but never a viewer: the params carry a
		// keyed digest, not an address. Log the type only, at Warn — a burst of
		// these is a database problem, not a playback problem.
		s.logger.WarnContext(ctx, "qoe event dropped (telemetry is best-effort; playback is unaffected)",
			"event_type", string(e.Type), "error", err)
		if s.metrics != nil {
			s.metrics.IncQoEDrop(string(e.Type))
		}
	}
}

// RollUp aggregates every complete hour that has not been aggregated yet, and
// returns how many hours it wrote.
//
// It is idempotent by construction: the raw rows of a complete hour never
// change (received_at is the server's own clock, so nothing can arrive late for
// a closed hour), and the upsert assigns rather than adds. Leadership moving
// mid-sweep therefore costs a recomputation, not a double count.
func (s *Service) RollUp(ctx context.Context, now time.Time) (int, error) {
	if s == nil || s.repo == nil {
		return 0, ErrUnavailable
	}
	completeBefore := HourBucket(now.Add(-bucketGrace))

	// Where to resume. The watermark is the newest hour already rolled up; with
	// none, start at the raw-retention floor, which is the oldest hour any
	// surviving event can be in.
	after := HourBucket(now.Add(-RawRetention))
	latest, err := s.repo.LatestQoERollupHour(ctx)
	if err != nil {
		return 0, err
	}
	if len(latest) > 0 {
		if next := HourBucket(latest[0]).Add(time.Hour); next.After(after) {
			after = next
		}
	}
	if !after.Before(completeBefore) {
		return 0, nil
	}

	buckets, err := s.repo.ListQoERollupBuckets(ctx, sqlcgen.ListQoERollupBucketsParams{
		AfterBucket:    after,
		CompleteBefore: completeBefore,
		MaxBuckets:     maxBucketsPerSweep,
	})
	if err != nil {
		return 0, err
	}
	done := 0
	for _, bucket := range buckets {
		if err := s.rollUpBucket(ctx, HourBucket(bucket)); err != nil {
			return done, err
		}
		done++
	}
	return done, nil
}

// rollUpBucket reads one hour of raw events by keyset page and writes one rollup
// row per (source, engine, format) group present in it.
func (s *Service) rollUpBucket(ctx context.Context, bucket time.Time) error {
	end := bucket.Add(time.Hour)
	groups := map[groupKey]*accumulator{}

	// The cursor is (received_at, id). Seeding it one microsecond before the
	// bucket — Postgres timestamptz resolution — rather than at the bucket start
	// is what makes the strict > comparison include a row landing exactly on the
	// boundary. Seeding it AT the bucket start with the nil UUID would skip such
	// a row if its id sorted at or below the nil UUID.
	cursorAt := bucket.Add(-time.Microsecond)
	cursorID := uuid.Nil
	for {
		rows, err := s.repo.ListQoEEventsForBucketPage(ctx, sqlcgen.ListQoEEventsForBucketPageParams{
			BucketStart:     bucket,
			BucketEnd:       end,
			AfterReceivedAt: cursorAt,
			AfterID:         cursorID,
			PageSize:        rollupPageSize,
		})
		if err != nil {
			return err
		}
		for i := range rows {
			row := rows[i]
			key := groupKey{
				source: row.DeliverySource,
				engine: row.Engine,
				format: row.PackagingFormat,
			}
			acc := groups[key]
			if acc == nil {
				acc = newAccumulator()
				groups[key] = acc
			}
			acc.observe(row)
			cursorAt, cursorID = row.ReceivedAt, row.ID
		}
		if int32(len(rows)) < rollupPageSize {
			break
		}
	}

	for key, acc := range groups {
		if err := s.repo.UpsertQoERollup(ctx, acc.params(bucket, key)); err != nil {
			return err
		}
	}
	return nil
}

// groupKey is the rollup's compound key minus the hour. Every member is drawn
// from a closed vocabulary, so the map has a hard size ceiling.
type groupKey struct {
	source string
	engine string
	format string
}

// accumulator is one group's running aggregate. Memory is O(1) in the number of
// events: the histograms are fixed-width count vectors, so an hour with a
// billion events costs the same as an hour with ten.
type accumulator struct {
	events        int64
	starts        int64
	rebuffers     int64
	switches      int64
	errs          int64
	verified      int64
	rebufferTotal int64
	ttff          *Histogram
	rebuf         *Histogram
	errorCounts   map[string]int64
}

func newAccumulator() *accumulator {
	return &accumulator{
		ttff:        NewHistogram(),
		rebuf:       NewHistogram(),
		errorCounts: map[string]int64{},
	}
}

func (a *accumulator) observe(row sqlcgen.ListQoEEventsForBucketPageRow) {
	a.events++
	if row.SessionVerified {
		a.verified++
	}
	switch EventType(row.EventType) {
	case EventStart:
		a.starts++
	case EventRebuffer:
		a.rebuffers++
	case EventBitrateSwitch:
		a.switches++
	case EventError:
		a.errs++
		if row.ErrorClass != nil && ValidErrorClass(ErrorClass(*row.ErrorClass)) {
			a.errorCounts[*row.ErrorClass]++
		}
	}
	if row.TtffMs != nil {
		a.ttff.Observe(int64(*row.TtffMs))
	}
	if row.RebufferMs != nil {
		a.rebuf.Observe(int64(*row.RebufferMs))
		a.rebufferTotal += int64(*row.RebufferMs)
	}
}

func (a *accumulator) params(bucket time.Time, key groupKey) sqlcgen.UpsertQoERollupParams {
	counts, err := json.Marshal(a.errorCounts)
	if err != nil {
		counts = []byte(`{}`)
	}
	return sqlcgen.UpsertQoERollupParams{
		HourBucket:         bucket,
		DeliverySource:     key.source,
		Engine:             key.engine,
		PackagingFormat:    key.format,
		EventCount:         a.events,
		StartCount:         a.starts,
		RebufferCount:      a.rebuffers,
		BitrateSwitchCount: a.switches,
		ErrorCount:         a.errs,
		VerifiedCount:      a.verified,
		TtffP50Ms:          a.ttff.Quantile(0.50),
		TtffP95Ms:          a.ttff.Quantile(0.95),
		TtffP99Ms:          a.ttff.Quantile(0.99),
		RebufferP50Ms:      a.rebuf.Quantile(0.50),
		RebufferP95Ms:      a.rebuf.Quantile(0.95),
		RebufferP99Ms:      a.rebuf.Quantile(0.99),
		RebufferTotalMs:    a.rebufferTotal,
		HistogramVersion:   HistogramVersion,
		TtffHistogram:      a.ttff.Counts(),
		RebufferHistogram:  a.rebuf.Counts(),
		ErrorCounts:        counts,
	}
}

// Prune applies the retention policy: raw measurements for RawRetention, rollups
// for RollupRetention. Both are deleted oldest-first in bounded batches, and
// nothing that is still inside its window is ever touched — the equivalent of
// jobstatus.Prune's "active work is never deleted", except that here the thing
// that must not be deleted is an hour that has not been rolled up yet.
//
// That last point is why RawRetention (7d) is so much longer than the rollup
// cadence (hourly): the raw window has to be wide enough that an instance which
// was down for days still finds its unrolled hours when it comes back. It is
// not, and cannot be made, a guarantee for an outage longer than the window —
// which is recorded here rather than hidden, because the failure mode is silent.
func (s *Service) Prune(ctx context.Context, now time.Time) (events, rollups int64, err error) {
	if s == nil || s.repo == nil {
		return 0, 0, ErrUnavailable
	}
	events, err = pruneLoop(ctx, func(ctx context.Context) (int64, error) {
		return s.repo.PruneQoEEvents(ctx, sqlcgen.PruneQoEEventsParams{
			Cutoff: now.Add(-RawRetention), BatchSize: pruneBatchSize,
		})
	})
	if err != nil {
		return events, 0, err
	}
	rollups, err = pruneLoop(ctx, func(ctx context.Context) (int64, error) {
		return s.repo.PruneQoERollups(ctx, sqlcgen.PruneQoERollupsParams{
			Cutoff: now.Add(-RollupRetention), BatchSize: pruneBatchSize,
		})
	})
	return events, rollups, err
}

// pruneLoop runs batches until one comes back short (nothing left) or the batch
// cap is reached, whichever is first.
func pruneLoop(ctx context.Context, batch func(context.Context) (int64, error)) (int64, error) {
	var total int64
	for i := 0; i < pruneMaxBatches; i++ {
		n, err := batch(ctx)
		total += n
		if err != nil {
			return total, err
		}
		if n < int64(pruneBatchSize) {
			return total, nil
		}
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
	}
	return total, nil
}

func optionalUUID(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}
