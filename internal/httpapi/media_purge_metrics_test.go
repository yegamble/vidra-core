package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// Purge success used to be fully silent and failure one aggregate log warn —
// yet promoting any media header to a shared-cacheable directive is gated on
// purge being "exercised against a live edge", which an operator could not
// observe short of grepping logs. These tests pin the counters and their admin
// surface.
//
// The counters are PROCESS-GLOBAL (the purge runs on a detached goroutine, and
// other tests in this package exercise it), so every assertion here is on
// DELTAS from a snapshot taken first, never on absolute values.
func TestSystemStatusCDNPurgeCounters(t *testing.T) {
	// No CDN wired: the block is ABSENT, not zeroed — a page reporting "0
	// purge runs" on an install with no edge reads as a purge system that
	// never works.
	t.Run("omitted without a CDN", func(t *testing.T) {
		srv := authServer(t)
		srv.lookPath = ffmpegFound
		if body := systemStatus(t, srv); body.CDNPurge != nil {
			t.Errorf("cdn_purge = %+v with no CDN wired, want the block omitted", body.CDNPurge)
		}
	})

	t.Run("counts runs, keys and the last incomplete run", func(t *testing.T) {
		srv := authServer(t)
		srv.lookPath = ffmpegFound
		WithDeliveryCDN(
			func(context.Context, string) (string, bool, error) { return "", false, nil },
			func(context.Context, string) error { return nil },
		)(srv)
		// One account for every read below — the page is read three times and
		// registering "ada" twice is a 409 (same shape as the pool-stats test).
		admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
		read := func() systemStatusResponse {
			t.Helper()
			rec := getWithAuth(srv, "/api/v1/admin/system", admin)
			if rec.Code != http.StatusOK {
				t.Fatalf("system status = %d; body=%s", rec.Code, rec.Body.String())
			}
			var body systemStatusResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			return body
		}

		base := read()
		if base.CDNPurge == nil {
			t.Fatal("cdn_purge block missing with a CDN wired")
		}

		// A clean run moves runs and keys_purged and nothing else.
		before := time.Now().Add(-time.Second)
		recordVideoEdgePurgeRun(4, 0, true)
		clean := read()
		if got, want := clean.CDNPurge.Runs-base.CDNPurge.Runs, int64(1); got != want {
			t.Errorf("runs delta after a clean run = %d, want %d", got, want)
		}
		if got, want := clean.CDNPurge.KeysPurged-base.CDNPurge.KeysPurged, int64(4); got != want {
			t.Errorf("keys_purged delta = %d, want %d", got, want)
		}
		if clean.CDNPurge.KeysFailed != base.CDNPurge.KeysFailed {
			t.Errorf("keys_failed moved on a clean run: %d -> %d", base.CDNPurge.KeysFailed, clean.CDNPurge.KeysFailed)
		}

		// An incomplete run (failures, or a key set known to be short) stamps
		// the timestamp an operator checks after a takedown.
		recordVideoEdgePurgeRun(2, 3, false)
		bad := read()
		if got, want := bad.CDNPurge.KeysFailed-clean.CDNPurge.KeysFailed, int64(3); got != want {
			t.Errorf("keys_failed delta = %d, want %d", got, want)
		}
		if bad.CDNPurge.LastIncompleteRunAt == nil {
			t.Fatal("last_incomplete_run_at missing after an incomplete run")
		}
		if bad.CDNPurge.LastIncompleteRunAt.Before(before) {
			t.Errorf("last_incomplete_run_at = %v, want a fresh stamp", bad.CDNPurge.LastIncompleteRunAt)
		}
	})
}

// The record function's own contract: complete-with-failures and
// incomplete-without-failures BOTH count as incomplete runs — "purged
// everything I knew about and some calls failed" and "the key list itself was
// short" are different facts that end in the same operator action (assume the
// edge may still serve the video).
func TestRecordVideoEdgePurgeRunStampsBothIncompleteShapes(t *testing.T) {
	for name, run := range map[string]func(){
		"failures":       func() { recordVideoEdgePurgeRun(1, 1, true) },
		"short key list": func() { recordVideoEdgePurgeRun(1, 0, false) },
	} {
		t.Run(name, func(t *testing.T) {
			before := time.Now().Add(-time.Second)
			run()
			_, _, _, last := videoEdgePurgeCounters()
			if last == nil || last.Before(before) {
				t.Errorf("last incomplete stamp = %v, want fresh", last)
			}
		})
	}
}
