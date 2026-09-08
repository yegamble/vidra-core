package httpapi

import (
	"context"
	"sync"
	"time"
)

// The `live_ingest` component — the RTMP plane on the status page and on
// readiness.
//
// A26's finding, verbatim: "/admin/system has no live or ingest component at
// all: ten components and not one watches the RTMP plane, so a dead ingest
// leaves the instance reading ok while every publish is refused and every viewer
// stares at a dead player." /admin/infrastructure echoes `live.rtmp_url` and a
// `configured: true` flag, but that is config, not a probe.
//
// THE VERDICTS:
//
//	not_configured  LIVE_RTMP_URL is unset — this install does not do live, which
//	                is a supported deployment and not a fault. Also the verdict
//	                when live IS configured but no control URL was given: there
//	                is an ingest, and this instance has no way to ask it
//	                anything, which is honestly "unknown" rather than "ok".
//	ok              the ingest's stat page answered.
//	down            it did not.
//
// IT NEVER TAKES THE API OUT OF ROTATION. Only PostgreSQL does that (handleReady
// makes the argument), and the reasoning applies with more force here: every
// replica shares one ingest, so 503ing on its death would empty the whole fleet
// out of rotation and take the VOD platform down with the live plane. A dead
// ingest surfaces as `degraded` in the readiness body with a 200 — visible to an
// operator, invisible to the balancer.

// liveIngestProbeTTL is how long one ingest probe answers for. /readyz is polled
// several times a minute forever and this component is on the CHEAP probe, so
// the round trip has to be amortised; thirty seconds keeps a dead ingest visible
// inside one status-page refresh while costing at most two probes a minute.
const liveIngestProbeTTL = 30 * time.Second

// liveIngestProber is the read side of the ingest control surface as this file
// needs it: one question, "are you there". *live.HTTPIngestController satisfies
// it; a test pins a verdict with three lines and no nginx.
type liveIngestProber interface {
	Probe(ctx context.Context) error
}

// liveIngestHealth caches one prober's verdict.
type liveIngestHealth struct {
	prober liveIngestProber

	mu     sync.Mutex
	at     time.Time
	err    error
	probed bool
}

// liveIngestStatus reports the component. Safe on a Server with no ingest wired.
func (s *Server) liveIngestStatus(ctx context.Context) componentStatus {
	// No live plane at all: not a fault. This is the same rule "not_configured"
	// carries everywhere else on the page — local storage, mail off, no search.
	if s.cfg == nil || s.cfg.LiveRTMPURL == "" {
		return componentStatus{Status: "not_configured"}
	}
	if s.liveIngest == nil || s.liveIngest.prober == nil {
		// There IS an ingest and no way to reach it. Deliberately not "ok": the
		// whole finding this component answers is an instance reporting health
		// it never measured.
		return componentStatus{
			Status: "not_configured",
			Error:  "live is configured but LIVE_INGEST_CONTROL_URL is not, so the RTMP ingest is never probed and a moderator cannot disconnect a publisher",
		}
	}
	if err := s.liveIngest.check(ctx); err != nil {
		return componentStatus{Status: "down", Error: "the RTMP ingest did not answer its control surface; publishes will be refused and live viewers will see a dead player"}
	}
	return componentStatus{Status: "ok"}
}

// check probes at most once per liveIngestProbeTTL and returns the cached
// verdict in between. Concurrent callers share one probe, exactly as readiness
// itself does.
func (h *liveIngestHealth) check(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	if h.probed && now.Sub(h.at) < liveIngestProbeTTL {
		return h.err
	}
	h.err = h.prober.Probe(ctx)
	h.at = now
	h.probed = true
	return h.err
}
