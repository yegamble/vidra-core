package httpapi

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vidra/vidra-core/internal/preflight"
	"github.com/vidra/vidra-core/internal/processheartbeat"
)

// systemProbeTimeout bounds EACH dependency probe on the admin status page. The
// probes run concurrently, so the page's worst case is one timeout rather than
// the sum of four: a mail relay that accepts a connection and then stops talking
// must not hold up the object-store answer.
const systemProbeTimeout = 3 * time.Second

// bucketChecker is the media backend that can answer "is my bucket there" in one
// authenticated round trip. Only the S3 backend implements it, so the assertion
// IS the "is this deployment on object storage" question — a local-filesystem
// instance has no remote store to be down.
type bucketChecker interface {
	BucketExists(ctx context.Context) (bool, error)
}

// settingsSyncHealth is the read side of the settings-version poller
// (internal/settingsversion): when this replica last agreed with the shared
// counter, and the error keeping it stale now (nil when the last attempt
// succeeded). An interface here so httpapi depends on the one question the
// page asks, not on the poller type.
type settingsSyncHealth interface {
	Health() (lastSuccess time.Time, lastErr error)
}

// processFleetReader is the per-process heartbeat read side
// (internal/processheartbeat, migration 0133). An interface here so httpapi
// depends on the two questions the page asks — who is out there, and is any of
// them in trouble — rather than on the concrete reader.
type processFleetReader interface {
	List(ctx context.Context, now time.Time) ([]processheartbeat.Process, error)
	StaleWindow() time.Duration
}

// systemComponents is componentHealth (postgres, redis) plus the dependencies
// that are too expensive to ask on every orchestrator tick: the object store,
// the mail relay, the search service and the ffmpeg binary. /readyz keeps the
// cheap two-dependency contract deliberately — this runs when an admin opens a
// page, that runs several times a minute forever.
//
// "not_configured" NEVER degrades the instance: local storage, mail off and no
// search service are all supported deployments, not faults. "down" does.
func (s *Server) systemComponents(ctx context.Context) (map[string]componentStatus, bool) {
	// BOUND the two cheap pings too. They were the only unbounded work on this
	// page: a refused Redis dial cost it 3.4-5.2s (five dial attempts), and a
	// Redis that accepts a connection and then stops talking would hang the page
	// outright — the page an operator opens BECAUSE something is wrong. Three
	// seconds is the same budget the four probes below get, and a ping that
	// overruns it is reported as down with the deadline as its reason, which is
	// the honest answer: a dependency that cannot answer in three seconds is not
	// serving this instance either.
	//
	// Deliberately applied HERE and not inside componentHealth, which /readyz
	// also calls: that probe runs several times a minute forever and its timeout
	// belongs to the orchestrator's own deadline, not to this page's.
	healthCtx, cancelHealth := context.WithTimeout(ctx, systemProbeTimeout)
	defer cancelHealth()
	components, healthy := s.componentHealth(healthCtx)

	probes := map[string]func(context.Context) componentStatus{
		"s3":     s.probeObjectStore,
		"smtp":   s.probeSMTP,
		"search": s.probeSearch,
		"ffmpeg": s.probeFFmpeg,
	}
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for name, probe := range probes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, systemProbeTimeout)
			defer cancel()
			status := probe(pctx)

			mu.Lock()
			defer mu.Unlock()
			components[name] = status
			if status.Status == "down" {
				healthy = false
			}
		}()
	}
	wg.Wait()

	// The settings poller is a fifth component but NOT a fifth probe: its
	// health is an in-memory read of the record the poll loop already keeps, so
	// there is no round trip to bound and no goroutine to spend on it. The
	// FLEET half of it is one indexed read of a table this process also writes,
	// so it is bounded by the same probe timeout as the four above.
	syncStatus := s.settingsSyncStatus()
	if syncStatus.Status == "ok" {
		if fleetStatus, ok := s.fleetSettingsSyncStatus(ctx); ok {
			syncStatus = fleetStatus
		}
	}
	components["settings_sync"] = syncStatus
	if syncStatus.Status == "down" {
		healthy = false
	}
	return components, healthy
}

// fleetSettingsSyncStatus judges every process the instance can see, and reports
// the first one an operator should be told about. It is consulted only when THIS
// process's own poll is healthy: a local failure is the more urgent fact and
// already has a message that names it.
//
// A read failure here is reported as down rather than swallowed. The alternative
// is the exact defect this closes one level up — a page that cannot see the
// fleet quietly claiming the fleet is fine.
func (s *Server) fleetSettingsSyncStatus(ctx context.Context) (componentStatus, bool) {
	if s.processFleet == nil {
		return componentStatus{}, false
	}
	fctx, cancel := context.WithTimeout(ctx, systemProbeTimeout)
	defer cancel()
	processes, err := s.processFleet.List(fctx, time.Now())
	if err != nil {
		return componentStatus{
			Status: "down",
			Error:  "the per-process heartbeats could not be read, so this page cannot say whether the other processes in this deployment are healthy: " + err.Error(),
		}, true
	}
	fault, degraded := processheartbeat.Judge(processes)
	if !degraded {
		return componentStatus{}, false
	}
	return componentStatus{Status: "down", Error: fault.Reason}, true
}

// settingsSyncStatus reports the settings-version poller's health (core#115).
// Its failure mode is the one this page exists for: a replica whose every poll
// fails keeps serving the instance settings it booted with — nothing errors,
// nothing user-visible breaks, and an admin's own change "took" on whichever
// replica served the write. Not wired (single-process installs, worker-role
// processes, unit servers) is a supported shape and never degrades, per the
// convention above.
func (s *Server) settingsSyncStatus() componentStatus {
	if s.settingsSync == nil {
		return componentStatus{Status: "not_configured"}
	}
	lastSuccess, err := s.settingsSync.Health()
	if err == nil {
		return componentStatus{Status: "ok"}
	}
	// The sentence names the CONSEQUENCE as well as the cause: "connection
	// refused" alone sends an operator to fix the database and never re-check
	// the settings this replica kept serving in the meantime.
	msg := "the settings-version poll is failing, so this replica may be serving stale instance settings/documents/branding: " + err.Error()
	if !lastSuccess.IsZero() {
		msg = "the settings-version poll has been failing (last success " +
			time.Since(lastSuccess).Round(time.Second).String() +
			" ago), so this replica may be serving stale instance settings/documents/branding: " + err.Error()
	}
	return componentStatus{Status: "down", Error: msg}
}

// probeObjectStore asks the configured bucket whether it is there.
func (s *Server) probeObjectStore(ctx context.Context) componentStatus {
	backend, ok := s.media.(bucketChecker)
	if !ok {
		return componentStatus{Status: "not_configured"}
	}
	// BucketExists, never EnsureBucket: a diagnostic that creates the bucket it
	// could not find turns a typo in STORAGE_S3_BUCKET into a new, empty,
	// silently-wrong store.
	exists, err := backend.BucketExists(ctx)
	switch {
	case err != nil:
		return componentStatus{Status: "down", Error: err.Error()}
	case !exists:
		return componentStatus{Status: "down", Error: "the configured bucket does not exist"}
	default:
		return componentStatus{Status: "ok"}
	}
}

// probeSMTP dials the relay and reads its greeting — it never authenticates and
// never sends, so running it on every page load costs the relay one connection
// and cannot get the account rate-limited.
func (s *Server) probeSMTP(ctx context.Context) componentStatus {
	if !s.cfg.MailEnabled || s.cfg.SMTPHost == "" {
		return componentStatus{Status: "not_configured"}
	}
	addr := net.JoinHostPort(s.cfg.SMTPHost, strconv.Itoa(s.cfg.SMTPPort))
	banner, err := preflight.CheckSMTP(ctx, addr)
	switch {
	case err != nil:
		return componentStatus{Status: "down", Error: err.Error()}
	case !strings.HasPrefix(strings.TrimSpace(banner), "220"):
		// Something answered, but not a mail relay — a proxy or a captive portal
		// in front of one. Password resets fail exactly as if nothing were there.
		return componentStatus{Status: "down", Error: "the greeting was not an SMTP 220"}
	default:
		return componentStatus{Status: "ok"}
	}
}

// probeSearch asks the search service's own /readyz from inside the compose
// network, which is a better vantage than any host-side check: in production the
// service publishes no host port at all, and the api is the only thing that talks
// to it.
func (s *Server) probeSearch(ctx context.Context) componentStatus {
	if s.searchClient == nil {
		return componentStatus{Status: "not_configured"}
	}
	if err := s.searchClient.Ready(ctx); err != nil {
		return componentStatus{Status: "down", Error: err.Error()}
	}
	return componentStatus{Status: "ok"}
}

// probeFFmpeg answers "is the binary on the PATH", and nothing more. ffmpeg is
// baked into the api image, so a missing one is a broken image rather than a
// runtime state, and EXECUTING it would spend a process on a question the PATH
// lookup already answered.
func (s *Server) probeFFmpeg(context.Context) componentStatus {
	if _, err := s.lookPath("ffmpeg"); err != nil {
		return componentStatus{Status: "down", Error: "ffmpeg is not on the api's PATH"}
	}
	return componentStatus{Status: "ok"}
}
