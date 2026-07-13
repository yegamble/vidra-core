package searchclient

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// discardLogger silences the prober's transition lines in tests.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestProberStateMachine pins the asymmetric thresholds: the service is condemned
// only after proberDownThreshold (2) consecutive failures and forgiven after
// proberUpThreshold (1) success, and observe reports a transition exactly once per
// edge.
func TestProberStateMachine(t *testing.T) {
	p := newProber("http://search.local")
	if !p.healthy.Load() {
		t.Fatal("prober should start optimistic (healthy)")
	}

	// A single failure does NOT flip a healthy service (slow to condemn).
	if p.observe(false) {
		t.Error("first failure transitioned; want none (threshold is 2)")
	}
	if !p.healthy.Load() {
		t.Error("healthy flipped after one failure; want still healthy")
	}
	// The second consecutive failure condemns it, reporting the edge.
	if !p.observe(false) {
		t.Error("second consecutive failure did not report a transition")
	}
	if p.healthy.Load() {
		t.Error("still healthy after two failures; want unhealthy")
	}
	// Further failures while already unhealthy report no new transition.
	if p.observe(false) {
		t.Error("failure while unhealthy reported a spurious transition")
	}

	// A single success forgives it (fast to forgive), reporting the edge once.
	if !p.observe(true) {
		t.Error("recovery success did not report a transition")
	}
	if !p.healthy.Load() {
		t.Error("still unhealthy after a success; want healthy")
	}
	// Further successes while already healthy report no new transition.
	if p.observe(true) {
		t.Error("success while healthy reported a spurious transition")
	}

	// The down counter resets on any success: fail once, succeed, fail once must
	// NOT condemn (the two failures were not consecutive).
	p.observe(false)
	p.observe(true)
	if p.observe(false) {
		t.Error("non-consecutive failures condemned the service; the counter must reset on success")
	}
	if !p.healthy.Load() {
		t.Error("healthy flipped on non-consecutive failures")
	}
}

// TestProberJitterBounds asserts nextInterval stays within ±proberJitterFraction
// (±20%) of the base interval across the rand-source range.
func TestProberJitterBounds(t *testing.T) {
	const base = 15 * time.Second
	p := newProber("http://search.local")
	p.interval = base

	lo := time.Duration(float64(base) * (1 - proberJitterFraction))
	hi := time.Duration(float64(base) * (1 + proberJitterFraction))

	for _, r := range []float64{0, 0.25, 0.5, 0.75, 0.999999} {
		p.rand = func() float64 { return r }
		got := p.nextInterval()
		if got < lo || got > hi {
			t.Errorf("rand=%v: nextInterval=%v out of [%v,%v]", r, got, lo, hi)
		}
	}
	// The extremes hit the documented bounds: rand=0 → 0.8x, rand→1 → ~1.2x.
	p.rand = func() float64 { return 0 }
	if got := p.nextInterval(); got != lo {
		t.Errorf("rand=0: nextInterval=%v, want the low bound %v", got, lo)
	}
}

// TestProberProbeOnceDetectsDownAndRecovery drives probeOnce against a real
// /healthz that toggles 200↔503 and confirms the flag follows the thresholds.
func TestProberProbeOnceDetectsDownAndRecovery(t *testing.T) {
	var down atomic.Bool
	var healthzHits int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("prober hit %q, want /healthz", r.URL.Path)
		}
		healthzHits++
		if down.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	p := newProber(ts.URL)
	p.logger = discardLogger()
	ctx := context.Background()

	// Up: probing a healthy service keeps it healthy.
	p.probeOnce(ctx)
	if !p.healthy.Load() {
		t.Fatal("healthy service marked unhealthy")
	}
	// Down: one failed probe is not enough; two are.
	down.Store(true)
	p.probeOnce(ctx)
	if !p.healthy.Load() {
		t.Fatal("condemned after a single failed probe (threshold is 2)")
	}
	p.probeOnce(ctx)
	if p.healthy.Load() {
		t.Fatal("still healthy after two failed probes")
	}
	// Recovery: one good probe forgives.
	down.Store(false)
	p.probeOnce(ctx)
	if !p.healthy.Load() {
		t.Fatal("not forgiven after a successful probe")
	}
	if healthzHits == 0 {
		t.Fatal("prober never reached the server")
	}
}

// TestHealthyCombinesProberAndBreaker verifies Client.Healthy() is the AND of the
// prober flag and "no breaker open": a healthy prober with an open breaker is not
// healthy, and an unhealthy prober is not healthy regardless of breaker state.
func TestHealthyCombinesProberAndBreaker(t *testing.T) {
	c := New("http://search.local", testSecret)

	// Fresh client: prober optimistic, no breaker open → healthy.
	if !c.Healthy() {
		t.Fatal("fresh client should be Healthy()")
	}

	// Open the search-group breaker (5 consecutive failures) → not healthy, even
	// though the prober still reads up.
	for i := 0; i < breakerThreshold; i++ {
		c.breakers[groupSearch].failure()
	}
	if !c.ServiceProbeHealthy() {
		t.Error("prober flag should still be up (breaker is a separate signal)")
	}
	if c.Healthy() {
		t.Error("Healthy() should be false while a breaker is open")
	}

	// Close the breaker; prober still up → healthy again.
	c.breakers[groupSearch].success()
	if !c.Healthy() {
		t.Error("Healthy() should recover once the breaker closes")
	}

	// Prober down dominates regardless of breaker state.
	c.prober.healthy.Store(false)
	if c.Healthy() {
		t.Error("Healthy() should be false while the prober reports down")
	}
}
