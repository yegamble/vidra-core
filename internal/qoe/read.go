package qoe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The admin read model. It answers the phase-4 exit criterion — "an admin can
// see TTFF/rebuffer percentiles per source for the last 24h" — from the rollups
// alone, never from a scan of the raw table.
//
// Two projections come back from one query, because they answer two different
// questions and a page of one cannot produce the other:
//
//   - Sources is the criterion itself: one merged row per delivery source over
//     the WHOLE window, percentiles recomputed from the summed histograms. It is
//     never paged, because a page of a summary is not a summary.
//   - Buckets is the hourly detail behind it, paged, for a chart and for
//     narrowing down when something changed.

const (
	// MaxWindow bounds how far back one request may ask. Rollups keep 90 days,
	// so a longer window is answerable in principle — but the summary merges
	// every row in the window in memory, and a bound that is enforced is worth
	// more than a capability nobody uses. Ask for a narrower window, or ask
	// twice.
	MaxWindow = 7 * 24 * time.Hour

	// DefaultWindow is the exit criterion's window, and therefore the default.
	DefaultWindow = 24 * time.Hour

	// maxSummaryRows caps the rows one request will merge. The closed
	// vocabularies cap an hour at 72 rows, so this is ~70 hours of an instance
	// using every source, engine and format simultaneously — far past anything
	// real, and a hard stop rather than a slow page if it is ever reached.
	maxSummaryRows = 5000
)

// ErrWindowTooWide is returned when the requested window holds more rollup rows
// than one response will merge. The API edge turns it into a 400 that says to
// narrow the window, which is a fixable instruction rather than a timeout.
var ErrWindowTooWide = errors.New("qoe: window holds too many rollup rows; narrow it")

// Percentiles is a measurement's p50/p95/p99 in milliseconds. Each is nil when
// the window recorded no measurement of that kind — "nobody reported a rebuffer"
// must not render as "0 ms of rebuffering", which is the same number an admin
// would read as perfect delivery.
type Percentiles struct {
	P50Ms *int32 `json:"p50_ms"`
	P95Ms *int32 `json:"p95_ms"`
	P99Ms *int32 `json:"p99_ms"`
}

// Bucket is one hour of one (source, engine, format) combination.
type Bucket struct {
	HourBucket      time.Time       `json:"hour_bucket"`
	DeliverySource  DeliverySource  `json:"delivery_source"`
	Engine          Engine          `json:"engine"`
	PackagingFormat PackagingFormat `json:"packaging_format"`

	EventCount         int64 `json:"event_count"`
	StartCount         int64 `json:"start_count"`
	RebufferCount      int64 `json:"rebuffer_count"`
	BitrateSwitchCount int64 `json:"bitrate_switch_count"`
	ErrorCount         int64 `json:"error_count"`

	// VerifiedCount is how many of this hour's events carried a session id the
	// server could check against a signed playback token. Anything below
	// EventCount is client-asserted — see the package comment.
	VerifiedCount int64 `json:"verified_session_count"`

	TTFF            Percentiles `json:"ttff"`
	Rebuffer        Percentiles `json:"rebuffer"`
	RebufferTotalMs int64       `json:"rebuffer_total_ms"`

	ErrorCounts map[string]int64 `json:"error_counts"`

	// RenditionReportingSupported is false for native HLS, permanently. It is on
	// the wire so the admin page can render "unsupported" instead of a zero that
	// looks like flawless ABR.
	RenditionReportingSupported bool `json:"rendition_reporting_supported"`

	ComputedAt time.Time `json:"computed_at"`
}

// SourceSummary is one delivery source merged across the whole window.
type SourceSummary struct {
	DeliverySource DeliverySource `json:"delivery_source"`

	EventCount         int64 `json:"event_count"`
	StartCount         int64 `json:"start_count"`
	RebufferCount      int64 `json:"rebuffer_count"`
	BitrateSwitchCount int64 `json:"bitrate_switch_count"`
	ErrorCount         int64 `json:"error_count"`
	VerifiedCount      int64 `json:"verified_session_count"`

	// TTFF and Rebuffer are recomputed from the SUMMED histograms of every row
	// in the window, not averaged from the hourly percentiles. Averaging
	// percentiles produces a number with no statistical meaning; this one is
	// correct to the histogram's bucket resolution.
	TTFF            Percentiles `json:"ttff"`
	Rebuffer        Percentiles `json:"rebuffer"`
	RebufferTotalMs int64       `json:"rebuffer_total_ms"`

	ErrorCounts map[string]int64 `json:"error_counts"`

	// Engines lists which engines contributed, so an admin can see at a glance
	// whether a source's numbers are dominated by a client that cannot report
	// renditions.
	Engines []Engine `json:"engines"`

	// PartialPercentiles is true when at least one row in the window was written
	// by a different histogram version and had to be excluded from the merge.
	// Counts still include it; the percentiles do not. Saying so is the whole
	// point of versioning the histogram.
	PartialPercentiles bool `json:"partial_percentiles"`
}

// PlaybackHealth is the admin response.
type PlaybackHealth struct {
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`

	Sources []SourceSummary `json:"sources"`

	Buckets      []Bucket `json:"buckets"`
	BucketsTotal int64    `json:"buckets_total"`
	Limit        int      `json:"limit"`
	Offset       int      `json:"offset"`
}

// PlaybackHealth returns the window summary plus one page of hourly detail.
//
// start/end are half-open [start, end) and are snapped to hour boundaries,
// because that is the resolution the rollups exist at; asking for 13:37 would
// otherwise silently return the whole 13:00 hour and misreport its own window.
func (s *Service) PlaybackHealth(ctx context.Context, start, end time.Time, limit, offset int) (PlaybackHealth, error) {
	if s == nil || s.repo == nil {
		return PlaybackHealth{}, ErrUnavailable
	}
	start, end = HourBucket(start), HourBucket(end)
	if !start.Before(end) {
		return PlaybackHealth{}, fmt.Errorf("qoe: window start must precede end")
	}
	if end.Sub(start) > MaxWindow {
		return PlaybackHealth{}, ErrWindowTooWide
	}

	total, err := s.repo.CountQoERollups(ctx, sqlcgen.CountQoERollupsParams{
		WindowStart: start, WindowEnd: end,
	})
	if err != nil {
		return PlaybackHealth{}, err
	}
	if total > maxSummaryRows {
		return PlaybackHealth{}, ErrWindowTooWide
	}

	// One read serves both projections. The summary needs every row in the
	// window and the page is a slice of the same set, so a second query would
	// only introduce the possibility of the two disagreeing.
	rows, err := s.repo.ListQoERollups(ctx, sqlcgen.ListQoERollupsParams{
		WindowStart: start, WindowEnd: end,
		ResultLimit: maxSummaryRows, ResultOffset: 0,
	})
	if err != nil {
		return PlaybackHealth{}, err
	}

	out := PlaybackHealth{
		WindowStart:  start,
		WindowEnd:    end,
		Sources:      summarize(rows),
		BucketsTotal: int64(len(rows)),
		Limit:        limit,
		Offset:       offset,
		Buckets:      []Bucket{},
	}
	for i := offset; i < len(rows) && len(out.Buckets) < limit; i++ {
		out.Buckets = append(out.Buckets, bucketFromRow(rows[i]))
	}
	return out, nil
}

// summarize merges every row into one entry per delivery source.
func summarize(rows []sqlcgen.QoeRollup) []SourceSummary {
	type agg struct {
		sum     SourceSummary
		ttff    *Histogram
		rebuf   *Histogram
		engines map[Engine]bool
	}
	bySource := map[DeliverySource]*agg{}
	for i := range rows {
		row := rows[i]
		src := DeliverySource(row.DeliverySource)
		a := bySource[src]
		if a == nil {
			a = &agg{
				sum:     SourceSummary{DeliverySource: src, ErrorCounts: map[string]int64{}},
				ttff:    NewHistogram(),
				rebuf:   NewHistogram(),
				engines: map[Engine]bool{},
			}
			bySource[src] = a
		}
		a.sum.EventCount += row.EventCount
		a.sum.StartCount += row.StartCount
		a.sum.RebufferCount += row.RebufferCount
		a.sum.BitrateSwitchCount += row.BitrateSwitchCount
		a.sum.ErrorCount += row.ErrorCount
		a.sum.VerifiedCount += row.VerifiedCount
		a.sum.RebufferTotalMs += row.RebufferTotalMs
		a.engines[Engine(row.Engine)] = true
		for class, n := range decodeErrorCounts(row.ErrorCounts) {
			a.sum.ErrorCounts[class] += n
		}
		if int(row.HistogramVersion) != HistogramVersion {
			// Counts above are still trustworthy — they are plain integers. The
			// distributions are not comparable across boundary tables, so they
			// are excluded and the exclusion is reported.
			a.sum.PartialPercentiles = true
			continue
		}
		if h, ok := FromCounts(row.TtffHistogram); ok {
			a.ttff.Merge(h)
		} else {
			a.sum.PartialPercentiles = true
		}
		if h, ok := FromCounts(row.RebufferHistogram); ok {
			a.rebuf.Merge(h)
		} else {
			a.sum.PartialPercentiles = true
		}
	}

	out := make([]SourceSummary, 0, len(bySource))
	for _, a := range bySource {
		a.sum.TTFF = percentilesOf(a.ttff)
		a.sum.Rebuffer = percentilesOf(a.rebuf)
		a.sum.Engines = sortedEngines(a.engines)
		out = append(out, a.sum)
	}
	// Stable, vocabulary order rather than "whichever the map yielded", so a
	// reload of the admin page does not reshuffle the table.
	order := map[DeliverySource]int{}
	for i, src := range DeliverySources {
		order[src] = i
	}
	sort.SliceStable(out, func(i, j int) bool {
		return order[out[i].DeliverySource] < order[out[j].DeliverySource]
	})
	return out
}

func percentilesOf(h *Histogram) Percentiles {
	return Percentiles{P50Ms: h.Quantile(0.50), P95Ms: h.Quantile(0.95), P99Ms: h.Quantile(0.99)}
}

func sortedEngines(set map[Engine]bool) []Engine {
	out := make([]Engine, 0, len(set))
	for _, e := range Engines {
		if set[e] {
			out = append(out, e)
		}
	}
	return out
}

func bucketFromRow(row sqlcgen.QoeRollup) Bucket {
	engine := Engine(row.Engine)
	return Bucket{
		HourBucket:                  row.HourBucket.UTC(),
		DeliverySource:              DeliverySource(row.DeliverySource),
		Engine:                      engine,
		PackagingFormat:             PackagingFormat(row.PackagingFormat),
		EventCount:                  row.EventCount,
		StartCount:                  row.StartCount,
		RebufferCount:               row.RebufferCount,
		BitrateSwitchCount:          row.BitrateSwitchCount,
		ErrorCount:                  row.ErrorCount,
		VerifiedCount:               row.VerifiedCount,
		TTFF:                        Percentiles{P50Ms: row.TtffP50Ms, P95Ms: row.TtffP95Ms, P99Ms: row.TtffP99Ms},
		Rebuffer:                    Percentiles{P50Ms: row.RebufferP50Ms, P95Ms: row.RebufferP95Ms, P99Ms: row.RebufferP99Ms},
		RebufferTotalMs:             row.RebufferTotalMs,
		ErrorCounts:                 decodeErrorCounts(row.ErrorCounts),
		RenditionReportingSupported: RenditionReportingSupported(engine),
		ComputedAt:                  row.ComputedAt.UTC(),
	}
}

// decodeErrorCounts reads the stored map, keeping only known classes. A stored
// row is written by this package, so an unknown key means a downgrade or a hand
// edit; dropping it keeps the response's vocabulary closed either way.
func decodeErrorCounts(raw []byte) map[string]int64 {
	out := map[string]int64{}
	if len(raw) == 0 {
		return out
	}
	var in map[string]int64
	if err := json.Unmarshal(raw, &in); err != nil {
		return out
	}
	for class, n := range in {
		if ValidErrorClass(ErrorClass(class)) && n > 0 {
			out[class] = n
		}
	}
	return out
}
