package httpapi

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/version"
)

// systemSoftware describes the running build for the admin status page.
type systemSoftware struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
}

// systemRateLimits is the effective, NON-SECRET rate-limit configuration
// surfaced read-only on the admin status page. Rate limits are a deploy-time
// capacity decision (config, not a runtime admin endpoint — see
// product-decisions §3): exposing them here lets an operator confirm what is in
// force without adding a runtime knob that a compromised admin could use to
// silently disable protection. No secret is involved (these are plain counts and
// a window), so it is safe to report.
type systemRateLimits struct {
	Enabled       bool  `json:"enabled"`
	Requests      int   `json:"requests"`       // general per-IP budget over the window
	AuthRequests  int   `json:"auth_requests"`  // stricter budget for auth endpoints
	WindowSeconds int64 `json:"window_seconds"` // the shared fixed window
}

// systemDatabase is this process's PostgreSQL pool, sampled when the page is
// read (phase-5 multi-node floor). Four counts and nothing else: they are the
// ones an operator can act on, and the invariant between them is what makes
// them readable — acquired + idle + constructing == total, and total can never
// exceed max, so "acquired pinned at max" is the whole diagnosis of a pool that
// has become the bottleneck.
//
// It is a SAMPLE, not history. The Prometheus gauges (vidra_db_pool_*) are the
// instrument for trend and for the wait counters; this block exists because the
// default install has no metrics stack, and an admin staring at a slow instance
// should not have to stand one up to find out whether its pool is full.
//
// Nothing here is a secret: connection COUNTS, never a DSN, a credential or a
// server address. The block is absent, not zeroed, when no pool is wired.
type systemDatabase struct {
	PoolTotalConns    int32 `json:"pool_total_conns"`
	PoolIdleConns     int32 `json:"pool_idle_conns"`
	PoolAcquiredConns int32 `json:"pool_acquired_conns"`
	PoolMaxConns      int32 `json:"pool_max_conns"` // DB_MAX_CONNS, per process
}

// systemStatusResponse is the admin-facing operational snapshot: build info, the
// runtime environment, process uptime, an overall health flag, per-dependency
// component status, the effective (non-secret) rate-limit config, and live
// connection-pool counts. It reports only operational metadata — never
// secrets/PII.
type systemStatusResponse struct {
	Status        string                     `json:"status"` // "ok" | "degraded" | "draining"
	Software      systemSoftware             `json:"software"`
	Environment   string                     `json:"environment"`
	UptimeSeconds int64                      `json:"uptime_seconds"`
	Components    map[string]componentStatus `json:"components"`
	RateLimits    systemRateLimits           `json:"rate_limits"`
	// Database is omitted entirely when no pool is wired (unit tests, embedders,
	// any process without a database). Omitting is the honest degradation: a
	// zeroed pool block would render as "0 of 0 connections", which an operator
	// reads as a pool with nothing left.
	Database *systemDatabase `json:"database,omitempty"`
}

// handleSystemStatus returns an operational snapshot for the admin dashboard.
// Behind requireRole(admin). Always 200 (even when degraded, even when
// draining) so the admin can see the state, unlike the /readyz probe which
// 503s.
func (s *Server) handleSystemStatus(c echo.Context) error {
	components, healthy := s.systemComponents(c.Request().Context())
	status := "ok"
	if !healthy {
		status = "degraded"
	}
	// Draining wins over both, exactly as it does on /readyz: once Drain() has
	// fired this process is leaving, and a dashboard reading "ok" while the
	// balancer reads 503 is two operators arguing about the same instance. The
	// page itself keeps serving through the drain delay — that window is
	// precisely when an admin is watching it.
	if s.draining.Load() {
		status = "draining"
	}
	return c.JSON(http.StatusOK, systemStatusResponse{
		Status:   status,
		Database: s.databasePoolSnapshot(),
		Software: systemSoftware{
			Name:      "vidra",
			Version:   version.Version,
			Commit:    version.Commit,
			BuildDate: version.Date,
			GoVersion: version.GoVersion(),
		},
		Environment:   s.cfg.Environment,
		UptimeSeconds: int64(time.Since(s.startedAt).Seconds()),
		Components:    components,
		RateLimits: systemRateLimits{
			Enabled:       s.cfg.RateLimitEnabled,
			Requests:      s.cfg.RateLimitRequests,
			AuthRequests:  s.cfg.AuthRateLimitRequests,
			WindowSeconds: int64(s.cfg.RateLimitWindow.Seconds()),
		},
	})
}

// databasePoolSnapshot samples the pool, or returns nil when none is wired.
// Nil is the whole nil-safety story for this block: the caller marshals it
// through an omitempty pointer, so an unwired process simply has no database
// section rather than a section full of zeroes.
func (s *Server) databasePoolSnapshot() *systemDatabase {
	if s.dbPoolStats == nil {
		return nil
	}
	st := s.dbPoolStats()
	return &systemDatabase{
		PoolTotalConns:    st.TotalConns,
		PoolIdleConns:     st.IdleConns,
		PoolAcquiredConns: st.AcquiredConns,
		PoolMaxConns:      st.MaxConns,
	}
}
