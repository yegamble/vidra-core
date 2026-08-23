package qoe

import "math"

// A latency histogram with fixed, code-owned bucket boundaries.
//
// # Why a histogram and not a sorted sample
//
// Exact percentiles need every sample. That has two costs the rollup worker
// cannot pay. The first is memory: an hour of playback on a busy instance is an
// unbounded slice per (source, engine, format) group, which is a worker that
// grows with traffic and eventually dies on the one day it matters most. The
// second is worse, and is the reason a sampling cap would not have fixed it
// either — the exit criterion is percentiles PER SOURCE for the last 24h, and a
// source's 24h spans 24 hourly rows times every engine and packaging format.
// Percentiles do not merge. The p95 of twenty-four p95s is not a statistic; it
// is a number that looks like one.
//
// Counts DO merge. Summing two histograms elementwise gives exactly the
// histogram of the union, so a window percentile is computed once, over the
// whole window, from rows that were each written once. That is the property that
// makes the admin endpoint cheap AND correct, and it costs O(1) worker memory.
//
// The price is resolution: a percentile is located within a bucket and
// interpolated across it, so it carries at most that bucket's relative width of
// error (see bucketGrowth). For "was TTFF 800 ms or 2,400 ms on the CDN
// yesterday" that is far below the noise floor of the thing being measured.
//
// # Versioning
//
// Every stored row carries HistogramVersion. Boundaries are a wire format the
// moment they are written to a table with 90-day retention: changing them
// without a version bump would silently reinterpret every existing row, and a
// reader that cannot recognise a version must refuse rather than guess.
const (
	// HistogramVersion pins the boundary table below. Bump it (and teach the
	// reader both) if the boundaries ever change.
	HistogramVersion = 1

	// bucketCount is the number of finite buckets plus one overflow bucket at
	// the end. Bucket i covers [bound(i-1), bound(i)); the last bucket is
	// [bound(n-2), +inf) and catches anything at or above the top boundary. The
	// top boundary sits above maxMeasurementMs, so Validate makes the overflow
	// bucket unreachable through ingest — it exists so that a corrupt stored row
	// or a merge with a future, wider version cannot lose counts.
	bucketCount = 112

	// bucketGrowth is the ratio between consecutive boundaries. 1.15 puts the
	// worst-case relative error at ~15% of a bucket before interpolation and
	// well under that after, while covering 1 ms to about an hour and a half in
	// 111 finite buckets — small enough that two of them per rollup row is a
	// rounding error against the row itself.
	bucketGrowth = 1.15
)

// bounds[i] is the exclusive upper edge of bucket i, for i in [0, bucketCount-1).
// bounds[0] is 1 ms, so bucket 0 is [0, 1) — a sub-millisecond measurement,
// which for TTFF means a cached start.
var bounds = func() []float64 {
	b := make([]float64, bucketCount-1)
	edge := 1.0
	for i := range b {
		b[i] = edge
		edge *= bucketGrowth
	}
	return b
}()

// Histogram is a fixed-boundary count vector. The zero value is not usable;
// build one with NewHistogram or FromCounts.
type Histogram struct {
	counts []int64
	total  int64
	sum    int64 // exact running total of observed values, in ms
}

// NewHistogram returns an empty histogram over the current boundaries.
func NewHistogram() *Histogram {
	return &Histogram{counts: make([]int64, bucketCount)}
}

// FromCounts adopts a stored count vector. A vector of the wrong length is
// rejected (ok=false) rather than padded or truncated: a short vector padded
// with zeros silently moves every percentile downward, which is exactly the kind
// of wrong answer that looks plausible. A nil/empty vector is a legitimate
// "no measurements" and yields an empty histogram.
func FromCounts(counts []int64) (*Histogram, bool) {
	if len(counts) == 0 {
		return NewHistogram(), true
	}
	if len(counts) != bucketCount {
		return nil, false
	}
	h := &Histogram{counts: make([]int64, bucketCount)}
	copy(h.counts, counts)
	for _, c := range counts {
		if c < 0 {
			return nil, false
		}
		h.total += c
	}
	return h, true
}

// Observe records one measurement in milliseconds. Negative values are ignored
// rather than clamped to zero — a negative duration is a broken client, and
// counting it as an instant start would flatter the p50.
func (h *Histogram) Observe(ms int64) {
	if ms < 0 {
		return
	}
	h.counts[bucketFor(ms)]++
	h.total++
	h.sum += ms
}

// Merge adds other's counts into h elementwise. Both must be the same version;
// the caller checks that before getting here.
func (h *Histogram) Merge(other *Histogram) {
	if other == nil {
		return
	}
	for i := range h.counts {
		h.counts[i] += other.counts[i]
	}
	h.total += other.total
	h.sum += other.sum
}

// Count is how many measurements the histogram holds.
func (h *Histogram) Count() int64 { return h.total }

// Sum is the exact total of the observed values in ms — exact because it is
// accumulated at Observe time, before bucketing loses resolution. It does not
// survive a round trip through FromCounts, which is why rebuffer_total_ms is its
// own stored column rather than something read back out of a histogram.
func (h *Histogram) Sum() int64 { return h.sum }

// Counts returns the stored representation.
func (h *Histogram) Counts() []int64 {
	out := make([]int64, len(h.counts))
	copy(out, h.counts)
	return out
}

// Quantile returns the q-th percentile in milliseconds (q in [0,1]), or nil when
// the histogram is empty. Nil is the honest answer for "nobody reported one":
// zero would say every viewer started instantly.
//
// The value is located by walking the cumulative counts to the bucket that holds
// rank ceil(q*total), then interpolating linearly across that bucket. Linear
// interpolation inside a log-spaced bucket is a deliberate simplification — it
// biases very slightly high within a bucket, which for a latency percentile is
// the safe direction.
func (h *Histogram) Quantile(q float64) *int32 {
	if h.total == 0 {
		return nil
	}
	if q < 0 {
		q = 0
	}
	if q > 1 {
		q = 1
	}
	rank := int64(math.Ceil(q * float64(h.total)))
	if rank < 1 {
		rank = 1
	}
	var cum int64
	for i, c := range h.counts {
		if c == 0 {
			continue
		}
		if cum+c < rank {
			cum += c
			continue
		}
		lo, hi := bucketRange(i)
		// Position within the bucket: which of this bucket's c samples the rank
		// falls on, spread evenly across [lo, hi).
		pos := float64(rank-cum) / float64(c)
		v := lo + (hi-lo)*pos
		out := int32(math.Round(v))
		if out < 0 {
			out = 0
		}
		return &out
	}
	// Unreachable while total equals the sum of counts, which Observe/FromCounts
	// both maintain. Falling through to the top edge rather than panicking keeps
	// a corrupt stored row from taking the admin page down.
	top := int32(math.Round(bounds[len(bounds)-1]))
	return &top
}

// bucketFor is the index whose range contains ms.
func bucketFor(ms int64) int {
	v := float64(ms)
	// Linear scan is fine: bucketCount is 97 and this runs once per event in a
	// paged read that is already dominated by the database round trip. A binary
	// search here would be a micro-optimisation on the wrong side of the wire.
	for i, edge := range bounds {
		if v < edge {
			return i
		}
	}
	return bucketCount - 1
}

// bucketRange is bucket i's [lo, hi). The overflow bucket's hi is one growth
// step beyond the top boundary so interpolation has a finite span; a value can
// only land there through a corrupt stored row, since Validate caps a
// measurement well below the top boundary.
func bucketRange(i int) (float64, float64) {
	var lo float64
	if i > 0 {
		lo = bounds[i-1]
	}
	if i < len(bounds) {
		return lo, bounds[i]
	}
	return lo, lo * bucketGrowth
}
