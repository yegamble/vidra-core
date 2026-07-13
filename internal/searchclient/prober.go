package searchclient

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// Health-prober tuning (MASTER-PLAN W9). The prober GETs the search service's
// PUBLIC /healthz on a jittered interval and flips a single health flag: it takes
// proberDownThreshold consecutive failures to condemn the service and
// proberUpThreshold successes to forgive it. The asymmetry (slow to condemn, fast
// to forgive) keeps a single dropped probe from flapping routing while resuming
// smart search the instant the service recovers.
const (
	proberDownThreshold   = 2
	proberUpThreshold     = 1
	defaultHealthInterval = 15 * time.Second
	proberJitterFraction  = 0.20
	proberProbeTimeout    = 5 * time.Second
)

// prober is the active health detector behind Client.Healthy(). It owns a single
// atomic health flag read on every routing decision; the background loop (run)
// is the sole writer, folding each probe result through observe. It starts
// optimistic (healthy) so a freshly-wired service is used immediately — the
// per-request fallback covers the brief window until the first probe lands if it
// is actually down. Build it via the Client options; the zero value is not usable.
type prober struct {
	url      string        // {baseURL}/healthz — the service's PUBLIC health surface
	http     *http.Client  // dedicated: a probe must never share/stall the API client
	interval time.Duration // base cadence (jittered per tick)
	timeout  time.Duration // per-probe deadline
	logger   *slog.Logger
	rand     func() float64 // [0,1) jitter source (test seam)

	healthy   atomic.Bool // current service health; the only field read concurrently
	failures  int         // consecutive probe failures (loop-goroutine only)
	successes int         // consecutive probe successes (loop-goroutine only)
}

// newProber builds an optimistic prober for baseURL's /healthz with the package
// defaults; Client options then override interval/logger/rand.
func newProber(baseURL string) *prober {
	p := &prober{
		url:      baseURL + "/healthz",
		http:     &http.Client{Timeout: proberProbeTimeout},
		interval: defaultHealthInterval,
		timeout:  proberProbeTimeout,
		logger:   slog.Default(),
		rand:     defaultJitterRand,
	}
	p.healthy.Store(true)
	return p
}

// observe folds one probe result into the health flag and reports whether the
// flag transitioned (so the loop logs + the gauge reflects exactly one edge per
// change). Down after proberDownThreshold consecutive failures; up after
// proberUpThreshold consecutive successes. Only ever called from run's goroutine,
// so the plain int counters need no lock.
func (p *prober) observe(ok bool) (transitioned bool) {
	if ok {
		p.failures = 0
		p.successes++
		if !p.healthy.Load() && p.successes >= proberUpThreshold {
			p.healthy.Store(true)
			return true
		}
		return false
	}
	p.successes = 0
	p.failures++
	if p.healthy.Load() && p.failures >= proberDownThreshold {
		p.healthy.Store(false)
		return true
	}
	return false
}

// nextInterval returns the base interval jittered by ±proberJitterFraction using
// the injected rand source, so a fleet of cores does not probe /healthz in
// lockstep. rand() in [0,1) maps the factor into [1-frac, 1+frac).
func (p *prober) nextInterval() time.Duration {
	factor := 1 + (p.rand()*2-1)*proberJitterFraction
	return time.Duration(float64(p.interval) * factor)
}

// probeOnce performs one GET /healthz under the probe deadline and folds the
// result (any 2xx = up; transport error, non-2xx, or timeout = down) into the
// health flag, emitting a single structured log line on a transition.
func (p *prober) probeOnce(ctx context.Context) {
	if !p.observe(p.check(ctx)) {
		return
	}
	if p.healthy.Load() {
		p.logger.InfoContext(ctx, "search service health recovered", "url", p.url, "healthy", true)
	} else {
		p.logger.WarnContext(ctx, "search service marked unhealthy", "url", p.url, "healthy", false)
	}
}

// check issues the /healthz GET under its own deadline and returns whether the
// service answered 2xx.
func (p *prober) check(ctx context.Context) bool {
	pctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, http.MethodGet, p.url, nil)
	if err != nil {
		return false
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	// Drain a little so an idle keep-alive connection can be reused next tick.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// run drives the prober until ctx is cancelled. It probes once immediately (a
// service that is down at boot is detected within ~2 intervals rather than after
// a full initial sleep) then on every jittered tick. Cancellation returns
// promptly — the prober never blocks shutdown.
func (p *prober) run(ctx context.Context) {
	if ctx.Err() == nil {
		p.probeOnce(ctx)
	}
	for {
		timer := time.NewTimer(p.nextInterval())
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			p.probeOnce(ctx)
		}
	}
}
