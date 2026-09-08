package httpapi

import (
	"strings"
	"testing"
	"time"
)

// The malware scanner is a runtime dependency like the object store and the mail
// relay: it can be configured and dead, and when it is, every upload and every
// URL import fails closed. Before this it had no component at all — a dead clamd
// left GET /api/v1/admin/system reporting `"status":"ok"` with nine healthy
// components while ingestion was refusing everything (measured in the A28 lab).
//
// "No scanner" is now TWO different facts and this page must tell them apart.
// The DECLARED one (MALWARE_SCAN_MODE=disabled) is supported and never degrades
// the instance — but it still says out loud what is not being scanned, because
// A28 found this posture discoverable only by an operator who happened to open
// /admin/infrastructure.
func TestSystemStatusScannerOptedOut(t *testing.T) {
	srv := authServer(t) // testConfig declares the opt-out
	srv.lookPath = ffmpegFound

	body := systemStatus(t, srv)

	c, ok := body.Components["clamav"]
	if !ok {
		t.Fatalf("clamav component missing from %+v", body.Components)
	}
	if c.Status != "not_configured" {
		t.Errorf("clamav status = %q, want not_configured", c.Status)
	}
	if !strings.Contains(c.Error, "MALWARE_SCAN_MODE=disabled") {
		t.Errorf("clamav error %q does not say the opt-out is in force", c.Error)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q; a DECLARED unscanned deployment is supported, not a fault", body.Status)
	}
}

// The UNDECLARED one — no scanner and no opt-out — is the posture nobody chose.
// Every ingestion route is answering 503, so the page degrades and names both
// levers. This is the state A28 measured as silently, invisibly unscanned.
func TestSystemStatusScannerUnconfiguredDegrades(t *testing.T) {
	cfg := testConfig()
	cfg.MalwareScanMode = ""
	srv := authServerWithConfig(t, cfg)
	srv.lookPath = ffmpegFound

	body := systemStatus(t, srv)

	c, ok := body.Components["clamav"]
	if !ok {
		t.Fatalf("clamav component missing from %+v", body.Components)
	}
	if c.Status != "degraded" {
		t.Errorf("clamav status = %q, want degraded", c.Status)
	}
	for _, want := range []string{"CLAMAV_ADDR", "MALWARE_SCAN_MODE=disabled", "scanner_not_configured"} {
		if !strings.Contains(c.Error, want) {
			t.Errorf("clamav error %q does not name %s", c.Error, want)
		}
	}
	if body.Status != "degraded" {
		t.Errorf("instance status = %q, want degraded", body.Status)
	}
}

// A configured scanner that cannot be reached is DOWN, and it degrades the
// instance: while it is unreachable, fail-closed refuses every upload and
// import. The sentence has to name that consequence and the policy in force, so
// an operator reading only "connection refused" does not stop at the daemon.
func TestSystemStatusScannerDownDegrades(t *testing.T) {
	cfg := testConfig()
	cfg.MalwareScanEnabled = true
	cfg.MalwareScanMode = "fail-closed"
	// Port 1 on loopback: nothing listens, so the dial fails fast without
	// depending on the test machine's network.
	cfg.ClamAVAddr = "127.0.0.1:1"
	cfg.ClamAVTimeout = time.Second
	cfg.MalwareScanMode = "fail-closed"
	srv := authServerWithConfig(t, cfg)
	srv.lookPath = ffmpegFound

	body := systemStatus(t, srv)

	c, ok := body.Components["clamav"]
	if !ok {
		t.Fatalf("clamav component missing from %+v", body.Components)
	}
	if c.Status != "down" {
		t.Fatalf("clamav status = %q, want down", c.Status)
	}
	if !strings.Contains(c.Error, "fail-closed") {
		t.Errorf("clamav error %q does not name the policy in force", c.Error)
	}
	if !strings.Contains(c.Error, "import") || !strings.Contains(c.Error, "upload") {
		t.Errorf("clamav error %q does not name the consequence (uploads and imports)", c.Error)
	}
	if body.Status != "ok" && body.Status != "degraded" {
		t.Errorf("unexpected instance status %q", body.Status)
	}
	if body.Status == "ok" {
		t.Errorf("instance status = ok with a dead scanner; a down dependency must degrade it")
	}
}

// CLAMAV_ADDR must not reach an admin page: admin_infra_test forbids it there by
// name, and net.Dialer embeds the dialed address verbatim in its error, so the
// probe's reason sentence is exactly where it would come back.
func TestSystemStatusScannerDownDoesNotLeakTheAddress(t *testing.T) {
	cfg := testConfig()
	cfg.MalwareScanEnabled = true
	cfg.ClamAVAddr = "sentinel-clamav.internal:3310"
	cfg.ClamAVTimeout = time.Second
	cfg.MalwareScanMode = "fail-open"
	srv := authServerWithConfig(t, cfg)
	srv.lookPath = ffmpegFound

	body := systemStatus(t, srv)

	c := body.Components["clamav"]
	if c.Status != "down" {
		t.Fatalf("clamav status = %q, want down", c.Status)
	}
	if strings.Contains(c.Error, "sentinel-clamav.internal") {
		t.Errorf("the scanner reason leaks CLAMAV_ADDR: %q", c.Error)
	}
	// The fail-open wording is the one an operator must not miss: the same dial
	// failure is publishing unscanned media rather than refusing uploads.
	if !strings.Contains(c.Error, "WITHOUT being scanned") {
		t.Errorf("fail-open reason %q does not say media is going out unscanned", c.Error)
	}
}
