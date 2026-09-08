package httpapi

// CDN purge outcome counters (phase-5 item 3 follow-through).
//
// WHY THEY EXIST. runVideoEdgePurge (media_purge.go) is deliberately quiet:
// success is silent and failure is one aggregate log warn, because a per-key
// line would turn one takedown into thousands of identical entries. But
// promoting any media header to a shared-cacheable directive is gated on purge
// being "exercised against a live edge", and an operator cannot certify
// "exercised" by grepping logs for the absence of a warning. These counters are
// the observable record of that exercise, surfaced on GET /api/v1/admin/system
// as the cdn_purge block.
//
// WHY PACKAGE-LEVEL ATOMICS. The purge runs on a DETACHED goroutine
// (context.WithoutCancel, outliving the request that triggered it), so there is
// no request scope to hang state on; and the numbers are answers about the
// PROCESS's lifetime, exactly like the pool's acquire counters. Counts and one
// timestamp only — never keys and never URLs: object keys enumerate a video's
// layout and a purge URL template can carry the credential in its query string.
//
// LOSSY BY DESIGN across restarts, like every in-process counter on the status
// page: the block answers "has purge been exercised, and is it failing NOW",
// not "how many purges ever ran" — the Prometheus stack is the instrument for
// history, when one exists.

import (
	"context"
	"sync/atomic"
	"time"
)

var (
	// videoEdgePurgeRuns counts attempted purge runs — one per video fan-out
	// (purgeVideoEdgeCopies) or single-key asset invalidation (purgeEdgeKey) —
	// not individual HTTP calls.
	videoEdgePurgeRuns atomic.Int64
	// videoEdgePurgeKeysPurged / videoEdgePurgeKeysFailed count the per-key
	// outcomes across all runs. "Purged" means the provider accepted the
	// invalidation (404 — never cached — is acceptance; see internal/cdn).
	videoEdgePurgeKeysPurged atomic.Int64
	videoEdgePurgeKeysFailed atomic.Int64
	// videoEdgePurgeLastIncompleteUnixNano is when a run last ended with the
	// edge possibly still serving something: per-key failures, or a key list
	// known to be short of what the edge could hold (a listing failed, or the
	// fan-out cap was hit). 0 = never. The two shapes are different facts with
	// the same operator action, so they share one stamp.
	videoEdgePurgeLastIncompleteUnixNano atomic.Int64
)

// recordVideoEdgePurgeRun files one purge run's outcome. Called from
// runVideoEdgePurge and purgeEdgeKey — the counters live here so that file's
// touches stay single calls.
func recordVideoEdgePurgeRun(purged, failed int, complete bool) {
	videoEdgePurgeRuns.Add(1)
	videoEdgePurgeKeysPurged.Add(int64(purged))
	videoEdgePurgeKeysFailed.Add(int64(failed))
	if failed > 0 || !complete {
		videoEdgePurgeLastIncompleteUnixNano.Store(time.Now().UnixNano())
	}
}

// videoEdgePurgeCounters snapshots the counters (tests, and the status page
// via cdnPurgeSnapshot). lastIncomplete is nil when every run so far purged
// its full key set.
func videoEdgePurgeCounters() (runs, keysPurged, keysFailed int64, lastIncomplete *time.Time) {
	runs = videoEdgePurgeRuns.Load()
	keysPurged = videoEdgePurgeKeysPurged.Load()
	keysFailed = videoEdgePurgeKeysFailed.Load()
	if nano := videoEdgePurgeLastIncompleteUnixNano.Load(); nano != 0 {
		t := time.Unix(0, nano)
		lastIncomplete = &t
	}
	return runs, keysPurged, keysFailed, lastIncomplete
}

// RecordEdgePurgeRun files one purge run's outcome from OUTSIDE this package.
//
// It exists for exactly one caller: internal/cdnpurge, which performs the
// immediate pass for the media-replacement hook and therefore has to reach the
// same counters this file owns. It is a package-level function rather than a
// method because those counters are process-wide (see WHY PACKAGE-LEVEL ATOMICS
// above) and because the caller is wired in cmd/api in EVERY role, including
// the worker-only one where no Server is constructed at all — there the counters
// simply accumulate unread, which is correct: a worker has no status page.
func RecordEdgePurgeRun(purged, failed int, complete bool) {
	recordVideoEdgePurgeRun(purged, failed, complete)
}

// systemCDNPurge is the admin status page's view of the purge seam. Absent —
// not zeroed — when no CDN is wired: "0 purge runs" on an install with no edge
// reads as a purge system that never works, when the truth is there is nothing
// to purge.
//
// TWO HALVES WITH DIFFERENT LIFETIMES, and the field names say which is which.
// runs/keys_purged/keys_failed/last_incomplete_run_at are this PROCESS's
// in-memory counters, reset by a restart — they answer "has purge been
// exercised, and is it failing now". pending_retries/oldest_pending_seconds/
// dead_letters are read from the durable queue (migration 0137) and answer a
// different question that a restart must not erase: "is the edge still serving
// something this instance has stopped serving". A takedown whose purge the edge
// refused shows up in the second half for hours, long after the log line that
// reported it has scrolled away.
type systemCDNPurge struct {
	Runs       int64 `json:"runs"`
	KeysPurged int64 `json:"keys_purged"`
	KeysFailed int64 `json:"keys_failed"`
	// LastIncompleteRunAt is the timestamp an operator checks after a takedown:
	// present iff some run since boot may have left the edge serving. Omitted
	// when clean, so "field absent" is the good news it reads as.
	LastIncompleteRunAt *time.Time `json:"last_incomplete_run_at,omitempty"`
	// PendingRetries is how many queued invalidations are still outstanding —
	// claimed or waiting on their backoff. Zero is the healthy reading.
	PendingRetries int64 `json:"pending_retries"`
	// OldestPendingSeconds is how long the oldest outstanding one has been
	// waiting. It is the STALENESS signal: a number that keeps growing means
	// the edge is refusing, and `vidra doctor` warns on it.
	OldestPendingSeconds int64 `json:"oldest_pending_seconds"`
	// DeadLetters is how many gave up after the attempt cap. Each one is an
	// edge still serving an object this instance no longer serves, and only a
	// manual invalidation at the provider clears it.
	DeadLetters int64 `json:"dead_letters"`
}

// cdnPurgeSnapshot returns the block, or nil when no CDN is wired — the same
// omitted-not-zeroed contract as databasePoolSnapshot, for the same reason.
//
// A queue read that FAILS leaves the durable half at zero rather than failing
// the status page: the page's job is to report what it can see, and a database
// that cannot answer is already the loudest component on it.
func (s *Server) cdnPurgeSnapshot(ctx context.Context) *systemCDNPurge {
	if !s.cdnConfigured() {
		return nil
	}
	runs, purged, failed, lastIncomplete := videoEdgePurgeCounters()
	block := &systemCDNPurge{
		Runs:                runs,
		KeysPurged:          purged,
		KeysFailed:          failed,
		LastIncompleteRunAt: lastIncomplete,
	}
	if queued, err := s.cdnpurgesvc.Stats(ctx); err == nil {
		block.PendingRetries = queued.Pending + queued.Running
		block.OldestPendingSeconds = int64(queued.OldestPendingAge / time.Second)
		block.DeadLetters = queued.DeadLettered
	}
	return block
}
