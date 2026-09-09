package httpapi

// TWIN: vidra-search internal/api/health.go — keep readiness semantics in sync.
// Both services answer the same orchestrator, so "ready" has to mean the same
// thing in both or a rollout gates on a different bar per service.

import (
	"context"
	"fmt"
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
	// Detail carries the small number of component-specific FACTS an operator
	// needs beside the verdict — today only the federation queue's last
	// successful delivery. It is omitted when empty, so every other component's
	// shape is byte-identical to what it was.
	//
	// The rehearsal is why it exists: FederationHealth.LastDeliveredAt is
	// computed on every probe and was rendered into nothing, so the one number
	// that separates "the queue is drained" from "the queue is abandoned" —
	// both of which read as zero pending — never reached the page that asks the
	// question.
	Detail map[string]string `json:"detail,omitempty"`
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
	components["mfa_kek"] = s.mfaKEKStatus()
	components["storage"] = s.storageWriteStatus()
	if components["storage"].Status == "down" {
		healthy = false
	}
	// The RTMP ingest (A26 — nothing here watched the live plane at all). It is
	// on the CHEAP probe rather than the admin page's expensive one because a
	// live instance whose ingest died is serving a dead player to every viewer
	// and refusing every publish, which is a thing an operator's dashboard must
	// see without anyone opening a page. Its own 30-second cache keeps the cost
	// to at most two round trips a minute however often /readyz is polled.
	//
	// It sets `healthy = false` so the readiness body reads "degraded" — and
	// deliberately does NOT change the status CODE: handleReady 503s for
	// PostgreSQL alone, and one shared ingest must never be able to empty every
	// replica out of rotation at once.
	components["live_ingest"] = s.liveIngestStatus(ctx)
	if components["live_ingest"].Status == "down" {
		healthy = false
	}
	// The IPFS mirror (A31 — /admin/system had no `ipfs` component at all, so an
	// unreachable node or gateway degraded nothing and the only honest signal was
	// one endpoint away on /ipfs/status). It is on the CHEAP probe, the one /readyz
	// runs several times a minute, for exactly the reason mfa_kek and storage are:
	// it costs no round trip. The probe itself runs on its own five-minute ticker
	// (ipfsmirror.Service.ProbeHealth, the cadence storage.WriteHealth established)
	// and this is a read of the record it keeps — a readiness check that fetched
	// from a gateway as often as the balancer asks would be the opposite trade.
	//
	// It sets `healthy = false` so the readiness body reads "degraded", and
	// deliberately does NOT change the status CODE: handleReady 503s for PostgreSQL
	// alone, and a gateway outage costs an instance nothing a viewer can see — the
	// api simply serves the bytes itself, which is the whole point of the gate the
	// same probe now feeds.
	if st, ok := s.ipfsStatus(); ok {
		components["ipfs"] = st
		if st.Status == "down" || st.Status == "degraded" {
			healthy = false
		}
	}
	if st, ok := s.privateIPFSStatus(); ok {
		components["private_ipfs"] = st
		if st.Status == "down" || st.Status == "degraded" {
			healthy = false
		}
	}
	return components, healthy
}

// storageWriteStatus reports whether this instance can still store a byte.
//
// It is on the CHEAP probe — the one /readyz runs several times a minute —
// for the reason mfa_kek is: it costs no round trip. The probe itself runs on
// its own five-minute ticker inside storage.WriteHealth, and this is a read of
// the record it keeps. Turning readiness into a PUT would be the opposite
// trade: a health check that writes to the object store as often as the
// balancer asks.
//
// It is "down" rather than "degraded" because a store that will not accept a
// write is not impaired, it is unable: every upload, every generated thumbnail,
// every caption and every transcode output fails. What that does to READINESS is
// a separate decision, and this file's existing convention makes it: only
// PostgreSQL takes an instance out of rotation (see handleReady). A storage
// refusal therefore reports the instance "degraded" with a 200 — exactly as a
// Redis outage does — because the instance still serves every read, every watch
// page and the admin console an operator needs in order to FIX it, and 503ing on
// a shared bucket's refusal would empty every replica out of rotation at once.
func (s *Server) storageWriteStatus() componentStatus {
	if s.storageWrite == nil {
		return componentStatus{Status: "not_configured"}
	}
	st := s.storageWrite.Status()
	switch {
	case !st.Probed:
		// Wired but not yet asked. Not a fault; the same rule as a nil Pinger.
		return componentStatus{Status: "not_configured"}
	case st.OK && st.Leaked:
		return componentStatus{
			Status: "degraded",
			Error:  "the object store accepts writes but would not let this instance delete its own scratch object, so media garbage collection and video deletion will leave objects behind. On Backblaze B2 deleteFiles is granted separately from writeFiles; grant the delete permission to the configured key.",
		}
	case st.OK:
		return s.storageMigrationTargetStatus()
	default:
		return componentStatus{Status: "down", Error: storageUnavailableMessage(st.Class)}
	}
}

// storageMigrationTargetStatus is the second half of the storage component: the
// MIGRATION TARGET's write verdict, reported only when the authoritative store
// is itself fine (a down store is the bigger fact and keeps the component).
//
// It is `degraded`, never `down`, and the distinction is the ruling. A34 found
// that a target this process could not write to was a FATAL BOOT REFUSAL — a
// rotated target credential took the whole instance offline, api and workers
// alike, over a store the instance needs only in order to finish a move. It now
// degrades instead: the campaign pauses, reads keep being served from whichever
// store holds authority, readiness stays 200 (this file's convention: only
// PostgreSQL takes an instance out of rotation), and the operator gets a
// sentence saying which of the two facts is true — the instance is working, the
// MOVE has stopped.
func (s *Server) storageMigrationTargetStatus() componentStatus {
	if s.storageMigrationTargetWrite == nil {
		return componentStatus{Status: "ok"}
	}
	st := s.storageMigrationTargetWrite.Status()
	if !st.Probed || st.OK {
		return componentStatus{Status: "ok"}
	}
	return componentStatus{
		Status: "degraded",
		Error: "this instance can store and serve media normally, but the STORAGE MIGRATION TARGET is not accepting writes (" +
			string(st.Class) + "), so the migration is paused: no objects are being copied and nothing is being deleted. " +
			"Fix the target store's credential or permissions; copying resumes on its own within five minutes of it accepting writes again, " +
			"or immediately if an admin resumes the campaign. Cancel the campaign if the move is no longer wanted.",
	}
}

// mfaKEKStatus reports the boot-time MFA-KEK sample (A37-2). It is on the CHEAP
// probe — the one /readyz runs several times a minute — because it costs no
// round trip at all: the sample was taken once at boot and the answer cannot
// change while the process lives.
//
// It is "degraded" and never "down", which is a deliberate decision rather than
// a softening. A KEK that cannot open this database's TOTP secrets breaks
// exactly one thing, the second-factor challenge; every password login, every
// read and every admin page still work. Taking the instance out of rotation
// would remove the operator's own way in — and the fix (put the right KEK back
// and restart) is only reachable through an api that is still serving.
func (s *Server) mfaKEKStatus() componentStatus {
	if s.mfaKEK == nil {
		return componentStatus{Status: "not_configured"}
	}
	if !s.mfaKEK.Mismatch() {
		return componentStatus{Status: "ok"}
	}
	// Counts only. Naming the accounts here would put a list of who holds a
	// second factor on an unauthenticated probe.
	return componentStatus{
		Status: "degraded",
		Error: fmt.Sprintf(
			"%d of the %d sampled TOTP secrets cannot be decrypted with the configured MFA_KEY_KEK, so those accounts' second factor can never verify (their challenge is refused exactly like a wrong code). The usual cause is a database restored without the config archive that carries the KEK, or with a different one. Recovery codes still work and an admin can reset a second factor; the fix is to put the KEK that sealed this database back in the environment and restart.",
			s.mfaKEK.Undecryptable, s.mfaKEK.Sealed),
	}
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
	case anyComponentDown(components), anyComponentDegraded(components):
		// Reachable database, something else down or impaired. Still serving —
		// and still 200, because the balancer's question is answered either way.
		// A DEGRADED component belongs in the top line too: a body that reads
		// "ok" while one of its components does not is a body an operator scans
		// past.
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

// anyComponentDegraded reports whether any probed dependency answered
// "degraded" — impaired but not a reason to stop routing here.
func anyComponentDegraded(components map[string]componentStatus) bool {
	for _, c := range components {
		if c.Status == "degraded" {
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
