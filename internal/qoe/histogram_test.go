package qoe

import (
	"math"
	"testing"
)

// relTolerance is the relative error a bucketed percentile is allowed. It is the
// bucket growth factor's half-width: a value is located inside a bucket whose
// edges are 15% apart and then interpolated across it, so it can be off by at
// most that, and in practice by much less. Asserting a tolerance rather than an
// exact value is not a weaker test — it is the honest specification of what a
// histogram promises, and a regression that widened the buckets would break it.
const relTolerance = 0.15

func assertQuantile(t *testing.T, h *Histogram, q float64, want float64) {
	t.Helper()
	got := h.Quantile(q)
	if got == nil {
		t.Fatalf("Quantile(%v) = nil, want ~%v", q, want)
	}
	if want == 0 {
		if *got > 1 {
			t.Errorf("Quantile(%v) = %d, want ~0", q, *got)
		}
		return
	}
	if rel := math.Abs(float64(*got)-want) / want; rel > relTolerance {
		t.Errorf("Quantile(%v) = %d, want ~%v (relative error %.3f > %.3f)", q, *got, want, rel, relTolerance)
	}
}

// TestHistogramPercentilesAgainstKnownDistribution feeds a distribution whose
// percentiles are known by construction — the integers 1..1000, one of each — so
// p50 is 500, p95 is 950 and p99 is 990 with no statistics involved.
func TestHistogramPercentilesAgainstKnownDistribution(t *testing.T) {
	h := NewHistogram()
	for i := 1; i <= 1000; i++ {
		h.Observe(int64(i))
	}
	if h.Count() != 1000 {
		t.Fatalf("Count = %d, want 1000", h.Count())
	}
	assertQuantile(t, h, 0.50, 500)
	assertQuantile(t, h, 0.95, 950)
	assertQuantile(t, h, 0.99, 990)
}

// TestHistogramPercentilesOnSkewedDistribution is the shape a real TTFF
// distribution has: a tight mass of fast starts and a long tail. The three
// populations are sized so each requested quantile lands in a DIFFERENT one —
// 885 fast, then 100 slow (ranks 886-985), then 15 pathological (986-1000) — so
// a p95 or p99 that silently tracked the mean, or the mode, would pass the
// uniform test above and fail here.
func TestHistogramPercentilesOnSkewedDistribution(t *testing.T) {
	h := NewHistogram()
	for i := 0; i < 885; i++ {
		h.Observe(400) // fast starts
	}
	for i := 0; i < 100; i++ {
		h.Observe(3000) // slow starts
	}
	for i := 0; i < 15; i++ {
		h.Observe(20000) // pathological starts
	}
	assertQuantile(t, h, 0.50, 400)   // rank 500 -> fast
	assertQuantile(t, h, 0.95, 3000)  // rank 950 -> slow
	assertQuantile(t, h, 0.99, 20000) // rank 990 -> pathological
}

// TestHistogramEmptyQuantileIsNil is the "no measurement is not zero" rule. A
// zero here would render on an admin page as perfect delivery.
func TestHistogramEmptyQuantileIsNil(t *testing.T) {
	h := NewHistogram()
	for _, q := range []float64{0, 0.5, 0.95, 0.99, 1} {
		if got := h.Quantile(q); got != nil {
			t.Errorf("empty Quantile(%v) = %d, want nil", q, *got)
		}
	}
}

// TestHistogramMergeEqualsUnion is the property the whole design rests on: the
// window summary sums the hourly histograms, and that has to give the same
// answer as having observed everything in one place. If this ever fails, every
// per-source 24h percentile in the admin view is wrong.
func TestHistogramMergeEqualsUnion(t *testing.T) {
	union := NewHistogram()
	a, b, c := NewHistogram(), NewHistogram(), NewHistogram()
	for i := 1; i <= 300; i++ {
		a.Observe(int64(i))
		union.Observe(int64(i))
	}
	for i := 301; i <= 700; i++ {
		b.Observe(int64(i))
		union.Observe(int64(i))
	}
	for i := 701; i <= 1000; i++ {
		c.Observe(int64(i))
		union.Observe(int64(i))
	}
	merged := NewHistogram()
	merged.Merge(a)
	merged.Merge(b)
	merged.Merge(c)

	if merged.Count() != union.Count() {
		t.Fatalf("merged Count = %d, want %d", merged.Count(), union.Count())
	}
	for _, q := range []float64{0.5, 0.95, 0.99} {
		want, got := union.Quantile(q), merged.Quantile(q)
		if want == nil || got == nil || *want != *got {
			t.Errorf("q=%v: merged = %v, union = %v — merging must be exact", q, deref(got), deref(want))
		}
	}
}

// TestHistogramRoundTripThroughCounts proves a stored row reads back as the same
// distribution, which is what the admin summary does on every request.
func TestHistogramRoundTripThroughCounts(t *testing.T) {
	h := NewHistogram()
	for i := 1; i <= 1000; i++ {
		h.Observe(int64(i))
	}
	back, ok := FromCounts(h.Counts())
	if !ok {
		t.Fatal("FromCounts rejected a histogram this package just produced")
	}
	if back.Count() != h.Count() {
		t.Fatalf("round-tripped Count = %d, want %d", back.Count(), h.Count())
	}
	for _, q := range []float64{0.5, 0.95, 0.99} {
		if *back.Quantile(q) != *h.Quantile(q) {
			t.Errorf("q=%v round-tripped to %d, want %d", q, *back.Quantile(q), *h.Quantile(q))
		}
	}
}

// TestFromCountsRejectsWrongWidth: a vector of the wrong length must be refused,
// not padded. Padding a short vector with zeros moves every percentile downward,
// which is a wrong answer that looks entirely plausible.
func TestFromCountsRejectsWrongWidth(t *testing.T) {
	if _, ok := FromCounts(make([]int64, bucketCount-1)); ok {
		t.Error("FromCounts accepted a short vector; it must refuse rather than pad")
	}
	if _, ok := FromCounts(make([]int64, bucketCount+1)); ok {
		t.Error("FromCounts accepted a long vector")
	}
	if h, ok := FromCounts(nil); !ok || h.Count() != 0 {
		t.Error("FromCounts(nil) must be an empty histogram, which is what a bucket with no measurements stores")
	}
}

// TestHistogramCoversTheValidatedRange: every value Validate admits must land in
// a finite bucket, so the overflow bucket is unreachable through ingest.
func TestHistogramCoversTheValidatedRange(t *testing.T) {
	if got := bucketFor(maxMeasurementMs); got == bucketCount-1 {
		t.Errorf("bucketFor(%d) landed in the overflow bucket; the boundaries must cover everything Validate admits", maxMeasurementMs)
	}
	if got := bucketFor(0); got != 0 {
		t.Errorf("bucketFor(0) = %d, want 0", got)
	}
}

// TestHistogramIgnoresNegative: a negative duration is a broken client, and
// counting it as an instant start would flatter the p50.
func TestHistogramIgnoresNegative(t *testing.T) {
	h := NewHistogram()
	h.Observe(-1)
	if h.Count() != 0 {
		t.Errorf("Count = %d after observing a negative value, want 0", h.Count())
	}
}

func deref(v *int32) any {
	if v == nil {
		return nil
	}
	return *v
}
