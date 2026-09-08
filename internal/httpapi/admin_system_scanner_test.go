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
// Not configured is a SUPPORTED deployment (MALWARE_SCAN_ENABLED=false) and must
// never degrade the instance, exactly like s3/smtp/search.
func TestSystemStatusScannerNotConfigured(t *testing.T) {
	srv := authServer(t)
	srv.lookPath = ffmpegFound

	body := systemStatus(t, srv)

	c, ok := body.Components["clamav"]
	if !ok {
		t.Fatalf("clamav component missing from %+v", body.Components)
	}
	if c.Status != "not_configured" {
		t.Errorf("clamav status = %q, want not_configured", c.Status)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q; an unscanned deployment is supported, not a fault", body.Status)
	}
}

// A configured scanner that cannot be reached is DOWN, and it degrades the
// instance: while it is unreachable, fail-closed refuses every upload and
// import. The sentence has to name that consequence and the policy in force, so
// an operator reading only "connection refused" does not stop at the daemon.
func TestSystemStatusScannerDownDegrades(t *testing.T) {
	cfg := testConfig()
	cfg.MalwareScanEnabled = true
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
