package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/version"
)

// livenessResponse is returned by GET /healthz.
type livenessResponse struct {
	Status string `json:"status"`
}

// componentStatus reports the health of a single dependency.
type componentStatus struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// readinessResponse is returned by GET /readyz.
//
// Status is one of:
//
//	ok          — everything this instance needs is reachable (HTTP 200)
//	degraded    — a NON-CRITICAL dependency is down; the instance is still
//	              serving, and a load balancer must keep routing to it (200)
//	unavailable — PostgreSQL is unreachable; nothing this api does works (503)
//	draining    — SIGTERM has been received and the listener is about to
//	              close; stop routing here (503)
//
// The HTTP status code is the load balancer's contract and the body is the
// operator's; `vidra status` renders both.
type readinessResponse struct {
	Status     string                     `json:"status"`
	Components map[string]componentStatus `json:"components"`
}

// readinessSnapshot is one cached probe result: the body and the code that go
// with it, plus when it was taken.
type readinessSnapshot struct {
	resp readinessResponse
	code int
	at   time.Time
}

// readinessCacheTTL is how long one probe result is reused. Short enough that a
// dependency outage is visible within one balancer health-check interval, long
// enough that N watchers cost the same as one.
const readinessCacheTTL = 2 * time.Second

// handleLive reports that the process is up and serving. It performs no
// dependency checks so an orchestrator can distinguish "process alive" from
// "ready to serve traffic".
func (s *Server) handleLive(c echo.Context) error {
	return c.JSON(http.StatusOK, livenessResponse{Status: "ok"})
}

// componentHealth pings the critical dependencies (postgres, redis) and reports
// each one's status plus an overall "all healthy" flag. Shared by readiness and
// the admin system-status endpoint. A nil Pinger reports "not_configured".
func (s *Server) componentHealth(ctx context.Context) (map[string]componentStatus, bool) {
	components := map[string]componentStatus{}
	healthy := true

	check := func(name string, p Pinger) {
		if p == nil {
			components[name] = componentStatus{Status: "not_configured"}
			return
		}
		if err := p.Ping(ctx); err != nil {
			healthy = false
			components[name] = componentStatus{Status: "down", Error: err.Error()}
			return
		}
		components[name] = componentStatus{Status: "ok"}
	}

	check("postgres", s.db)
	check("redis", s.rdb)
	return components, healthy
}

// handleReady answers the load balancer's question — "should I send this
// instance traffic?" — which is deliberately NOT the same question as "is
// everything about this instance fine".
//
// Two things follow, and both are the point of this handler.
//
// Only PostgreSQL takes an instance out of rotation. It is the system of
// record; without it essentially every route is a 500, so a replica that cannot
// reach it has nothing to offer. Redis does not: every rate limiter in front of
// this server FAILS OPEN on a Redis error (see ratelimit.go — it logs "rate
// limiter unavailable, failing open" and serves the request), and the other
// Redis users are caches. A Redis blip is therefore a degradation, and 503ing on
// it would take EVERY replica out of rotation SIMULTANEOUSLY — they all share
// the same Redis — turning a partial loss of rate limiting into a total outage.
// It is reported as a degraded component in the body, with a 200.
//
// Draining wins over everything. Once Drain() has been called this instance is
// leaving, and it says so before the listener closes so the balancer has a
// chance to notice.
func (s *Server) handleReady(c echo.Context) error {
	if s.draining.Load() {
		// Not cached and not probed: the answer does not depend on any
		// dependency, and spending a DB connection to decorate a 503 nobody will
		// route on would be the opposite of what draining is for.
		return c.JSON(http.StatusServiceUnavailable, readinessResponse{
			Status:     "draining",
			Components: map[string]componentStatus{},
		})
	}
	snap := s.readiness(c.Request().Context())
	return c.JSON(snap.code, snap.resp)
}

// readiness returns the probe result, taking a fresh one only when the cached
// one has aged out. Concurrent callers wait on the same lock and share the
// result rather than each opening their own probe: readiness is polled by
// everything in front of the instance, and the cost of answering it must not
// scale with how many things are watching.
func (s *Server) readiness(ctx context.Context) readinessSnapshot {
	now := time.Now()

	s.readinessMu.Lock()
	defer s.readinessMu.Unlock()
	if cached := s.readinessCached; cached != nil && now.Sub(cached.at) < readinessCacheTTL {
		return *cached
	}

	components, _ := s.componentHealth(ctx)
	snap := readinessSnapshot{
		resp: readinessResponse{Status: "ok", Components: components},
		code: http.StatusOK,
		at:   now,
	}
	switch {
	case components["postgres"].Status == "down":
		snap.resp.Status = "unavailable"
		snap.code = http.StatusServiceUnavailable
	case anyComponentDown(components):
		// Reachable database, something else down. Still serving.
		snap.resp.Status = "degraded"
	}
	s.readinessCached = &snap
	return snap
}

// anyComponentDown reports whether any probed dependency answered "down".
// "not_configured" is not a fault — an install with no Redis wired is a
// supported deployment, not a broken one.
func anyComponentDown(components map[string]componentStatus) bool {
	for _, c := range components {
		if c.Status == "down" {
			return true
		}
	}
	return false
}

// nodeInfoResponse is a minimal NodeInfo-style discovery document. It will be
// expanded toward the NodeInfo 2.1 schema as federation lands (PT-REST-OPENAPI).
type nodeInfoResponse struct {
	Version  string `json:"version"`
	Software struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"software"`
	Instance struct {
		Name string `json:"name"`
	} `json:"instance"`
}

// handleNodeInfo returns basic instance discovery metadata.
func (s *Server) handleNodeInfo(c echo.Context) error {
	var resp nodeInfoResponse
	resp.Version = "2.0"
	resp.Software.Name = "vidra"
	resp.Software.Version = version.Version
	resp.Instance.Name = s.cfg.InstanceName
	return c.JSON(http.StatusOK, resp)
}
