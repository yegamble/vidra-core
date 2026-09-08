package httpapi

import (
	"context"
	"errors"
	"testing"

	"github.com/vidra/vidra-core/internal/config"
)

type stubIngestProber struct {
	err   error
	calls int
}

func (s *stubIngestProber) Probe(context.Context) error { s.calls++; return s.err }

// TestLiveIngestComponentVerdicts walks the three answers, including the one
// that is the whole point: an instance with a live plane it cannot ask about
// must not report "ok". A26 measured /admin/system reading `"status":"ok"` while
// every publish was refused and every viewer stared at a dead player.
func TestLiveIngestComponentVerdicts(t *testing.T) {
	cfg := testConfig()

	t.Run("no live plane is not a fault", func(t *testing.T) {
		srv := &Server{cfg: cfg}
		if got := srv.liveIngestStatus(context.Background()); got.Status != "not_configured" {
			t.Errorf("status = %q, want not_configured", got.Status)
		}
	})

	t.Run("live configured with no control surface is not ok", func(t *testing.T) {
		c := *cfg
		c.LiveRTMPURL = "rtmp://ingest.invalid/live"
		srv := &Server{cfg: &c}
		got := srv.liveIngestStatus(context.Background())
		if got.Status == "ok" {
			t.Error("reported ok for a plane it never probed — the exact finding this component answers")
		}
		if got.Status != "not_configured" || got.Error == "" {
			t.Errorf("status = %+v, want not_configured with an explanation", got)
		}
	})

	t.Run("a live ingest that answers is ok", func(t *testing.T) {
		c := *cfg
		c.LiveRTMPURL = "rtmp://ingest.invalid/live"
		srv := &Server{cfg: &c, liveIngest: &liveIngestHealth{prober: &stubIngestProber{}}}
		if got := srv.liveIngestStatus(context.Background()); got.Status != "ok" {
			t.Errorf("status = %q, want ok", got.Status)
		}
	})

	t.Run("a dead ingest is down and says what it means", func(t *testing.T) {
		c := *cfg
		c.LiveRTMPURL = "rtmp://ingest.invalid/live"
		srv := &Server{cfg: &c, liveIngest: &liveIngestHealth{prober: &stubIngestProber{err: errors.New("dial tcp: connection refused")}}}
		got := srv.liveIngestStatus(context.Background())
		if got.Status != "down" {
			t.Errorf("status = %q, want down", got.Status)
		}
		if got.Error == "" {
			t.Error("a down ingest carried no explanation for the operator reading the page")
		}
		// The raw dial error names the ingest host. That belongs in the log, not
		// in a body an admin page renders and a screenshot travels with.
		if got.Error == "dial tcp: connection refused" {
			t.Error("the raw transport error was passed through; it can carry the ingest's address")
		}
	})
}

// TestLiveIngestProbeIsCached bounds the cost on /readyz, which is polled
// several times a minute forever.
func TestLiveIngestProbeIsCached(t *testing.T) {
	c := *testConfig()
	c.LiveRTMPURL = "rtmp://ingest.invalid/live"
	prober := &stubIngestProber{}
	srv := &Server{cfg: &c, liveIngest: &liveIngestHealth{prober: prober}}
	for i := 0; i < 25; i++ {
		srv.liveIngestStatus(context.Background())
	}
	if prober.calls != 1 {
		t.Errorf("%d probes for 25 readiness checks inside one %s window, want 1", prober.calls, liveIngestProbeTTL)
	}
}

var _ = config.Config{}
