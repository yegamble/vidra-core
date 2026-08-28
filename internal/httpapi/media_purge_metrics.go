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

// systemCDNPurge is the admin status page's view of the counters. Absent —
// not zeroed — when no CDN is wired: "0 purge runs" on an install with no edge
// reads as a purge system that never works, when the truth is there is nothing
// to purge.
type systemCDNPurge struct {
	Runs       int64 `json:"runs"`
	KeysPurged int64 `json:"keys_purged"`
	KeysFailed int64 `json:"keys_failed"`
	// LastIncompleteRunAt is the timestamp an operator checks after a takedown:
	// present iff some run since boot may have left the edge serving. Omitted
	// when clean, so "field absent" is the good news it reads as.
	LastIncompleteRunAt *time.Time `json:"last_incomplete_run_at,omitempty"`
}

// cdnPurgeSnapshot returns the block, or nil when no CDN is wired — the same
// omitted-not-zeroed contract as databasePoolSnapshot, for the same reason.
func (s *Server) cdnPurgeSnapshot() *systemCDNPurge {
	if !s.cdnConfigured() {
		return nil
	}
	runs, purged, failed, lastIncomplete := videoEdgePurgeCounters()
	return &systemCDNPurge{
		Runs:                runs,
		KeysPurged:          purged,
		KeysFailed:          failed,
		LastIncompleteRunAt: lastIncomplete,
	}
}
