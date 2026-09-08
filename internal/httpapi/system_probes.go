package httpapi

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/preflight"
	"github.com/vidra/vidra-core/internal/processheartbeat"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/video"
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

// storageWriteHealth is the read side of the object store's write probe
// (internal/storage.WriteHealth): the last verdict, already classified. An
// interface here so httpapi depends on the one question it asks — can this
// instance still store a byte — rather than on the monitor type, and so a test
// can pin a verdict without a store.
type storageWriteHealth interface {
	Status() storage.WriteStatus
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
// "not_configured" NEVER degrades the instance: local storage, mail off, no
// search service and a DECLARED malware-scan opt-out are all supported
// deployments, not faults. "down" and "degraded" do.
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
		"clamav": s.probeMalwareScanner,
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
			// "degraded" counts too. The scanner is the case that forced it: an
			// instance refusing every upload with 503 scanner_not_configured is
			// not healthy, and reporting the top line "ok" beside a degraded
			// component is exactly the reading an operator scans past (the same
			// argument /readyz's aggregation already makes).
			if status.Status == "down" || status.Status == "degraded" {
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

// probeSMTP walks the handshake a real send depends on — EHLO, STARTTLS if the
// relay offers it, AUTH if credentials are configured — and stops before MAIL
// FROM. It sends no message, so running it on every page load costs the relay
// one connection and cannot get the account rate-limited.
//
// Reading the 220 greeting, which is all this used to do, proved almost
// nothing. A05 pointed it at a relay whose certificate this instance refuses and
// it reported `smtp: ok` while every single send failed — and again while every
// send failed for want of AUTH. `ok` here now means a real send would get past
// the steps that break; anything short of that is `down`, with a sentence that
// names the stage, because "connection refused" and "certificate signed by
// unknown authority" send an operator to two completely different places.
func (s *Server) probeSMTP(ctx context.Context) componentStatus {
	if !s.cfg.MailEnabled || s.cfg.SMTPHost == "" {
		return componentStatus{Status: "not_configured"}
	}
	_, err := preflight.CheckSMTPHandshake(ctx, preflight.SMTPHandshake{
		Host:     s.cfg.SMTPHost,
		Port:     s.cfg.SMTPPort,
		Username: s.cfg.SMTPUsername,
		Password: s.cfg.SMTPPassword,
	})
	if err == nil {
		return componentStatus{Status: "ok"}
	}
	return componentStatus{Status: "down", Error: smtpProbeReason(err)}
}

// smtpProbeReason turns a handshake failure into the sentence an operator can
// act on. It names the CONSEQUENCE as well as the cause, the same discipline
// settingsSyncStatus uses: an operator who reads only the cause routinely fixes
// the wrong thing.
func smtpProbeReason(err error) string {
	var he *preflight.SMTPHandshakeError
	if !errors.As(err, &he) {
		return err.Error()
	}
	switch he.Stage {
	case preflight.SMTPStageDial:
		return "the mail relay could not be reached, so every password reset and verification message fails: " + underlying(he)
	case preflight.SMTPStageGreeting:
		// Something answered, but not a mail relay — a proxy or a captive portal
		// in front of one. Password resets fail exactly as if nothing were there.
		return "something answered on that port but it did not greet as an SMTP relay (a proxy or captive portal in front of the port answers exactly like this): " + underlying(he)
	case preflight.SMTPStageSTARTTLS:
		return "the relay advertises STARTTLS but the encrypted session could not be established, so every message fails before it is sent. Vidra verifies the certificate strictly against SMTP_HOST and has no skip-verify knob: use a certificate the host trust store accepts, or add the relay's CA to that store: " + underlying(he)
	case preflight.SMTPStageAuthUnsupported:
		return "SMTP_USERNAME is set but this relay offers no AUTH, so every send fails closed rather than going out unauthenticated. Either clear the credentials or point at the relay's submission port (587), which is usually the one that advertises AUTH"
	case preflight.SMTPStageAuth:
		return "the relay rejected the configured SMTP credentials, so every message fails before it is sent. Check SMTP_USERNAME and SMTP_PASSWORD: " + underlying(he)
	default:
		return "the mail relay handshake failed at the " + he.Stage + " step: " + underlying(he)
	}
}

// underlying renders the wrapped cause, or the whole error when there is none.
func underlying(he *preflight.SMTPHandshakeError) string {
	if he.Err == nil {
		return he.Error()
	}
	return he.Err.Error()
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

// probeMalwareScanner asks the configured clamd whether it is alive. It is the
// one dependency on this page whose failure is INVISIBLE everywhere else: with
// MALWARE_SCAN_MODE=fail-closed (the default) an unreachable daemon makes every
// upload and every URL import land in 'failed', while the api keeps answering
// 200 and /healthz keeps saying ok. /admin/infrastructure's malware_scan row is
// static config — it reports "enabled and configured" for a daemon that has
// been dead for a week.
//
// Running with no scanner is only supported when the operator SAID SO
// (MALWARE_SCAN_MODE=disabled): that reads not_configured with an honest
// sentence and never degrades the instance. No scanner and no opt-out is the
// posture nobody chose — every ingestion route is answering 503
// scanner_not_configured — so it degrades, because that is exactly the state an
// operator needs this page to surface.
//
// The sentence names the consequence and the policy in force, never the
// address: an internal host on an admin page is free reconnaissance, and
// admin_infra_test pins that rule for CLAMAV_ADDR specifically.
func (s *Server) probeMalwareScanner(ctx context.Context) componentStatus {
	if strings.TrimSpace(s.cfg.ClamAVAddr) == "" {
		if s.cfg.MalwareScanOptedOut() {
			return componentStatus{
				Status: "not_configured",
				Error:  "this instance runs with MALWARE_SCAN_MODE=disabled: uploads, imports, posters, avatars, banners and account archives are stored without being scanned. Point CLAMAV_ADDR at a ClamAV daemon and drop the disabled mode to turn scanning on",
			}
		}
		return componentStatus{
			Status: "degraded",
			Error:  scannerUnconfiguredReason,
		}
	}
	if err := media.Ping(ctx, s.cfg.ClamAVAddr, s.cfg.ClamAVTimeout); err != nil {
		return componentStatus{
			Status: "down",
			Error:  scannerProbeReason(s.cfg.MalwareScanMode, s.cfg.ClamAVAddr, err),
		}
	}
	return componentStatus{Status: "ok"}
}

// scannerUnconfiguredReason is the one sentence for "no scanner, no opt-out".
// It names both levers because either one resolves the state, and an operator
// who reads only half of it will pick the wrong one.
const scannerUnconfiguredReason = "no malware scanner is configured and MALWARE_SCAN_MODE is not disabled, so every upload, import and image upload is refused with 503 scanner_not_configured: point CLAMAV_ADDR at a ClamAV daemon, or set MALWARE_SCAN_MODE=disabled to ingest unscanned on purpose"

// scannerProbeReason spells out what an unreachable scanner is doing to
// ingestion RIGHT NOW, which is entirely decided by MALWARE_SCAN_MODE — the
// same failure publishes unscanned media under fail-open and rejects every
// upload under fail-closed, and an operator cannot infer which from a dial
// error.
func scannerProbeReason(mode, addr string, err error) string {
	// net.Dialer puts the dialed address verbatim into its error ("dial tcp
	// 10.0.0.4:3310: connect: connection refused"), so passing the cause through
	// would put CLAMAV_ADDR on an admin page — which admin_infra_test forbids by
	// name for exactly this value. The operator already knows what they
	// configured; the reachability verdict is the new information.
	cause := redactAddr(err.Error(), addr)
	switch video.ScanMode(mode) {
	case video.ScanModeFailOpen:
		return "the malware scanner is unreachable and MALWARE_SCAN_MODE=fail-open, so every upload and every URL import is being published WITHOUT being scanned: " + cause
	case video.ScanModeQuarantine:
		return "the malware scanner is unreachable and MALWARE_SCAN_MODE=quarantine, so every upload and every URL import is being parked in the moderation queue instead of published: " + cause
	default:
		return "the malware scanner is unreachable and MALWARE_SCAN_MODE=fail-closed, so every upload and every URL import is failing: " + cause
	}
}

// redactAddr removes a configured host:port — and the bare host, which is what a
// DNS failure reports on its own — from a dial error before it is shown.
func redactAddr(cause, addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return cause
	}
	cause = strings.ReplaceAll(cause, addr, "the configured address")
	if host, _, err := net.SplitHostPort(addr); err == nil && host != "" {
		cause = strings.ReplaceAll(cause, host, "the configured address")
	}
	return cause
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
