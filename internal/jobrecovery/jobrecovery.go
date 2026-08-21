// Package jobrecovery returns jobs stranded mid-flight by a dead worker to their
// queues.
//
// # How a job gets stranded, and how it comes back
//
// Every durable queue in vidra-core claims work by flipping a row to 'running'
// (channel_syncs: 'syncing') and pushing its due time forward by a fixed LEASE.
// The worker renews that lease on a ticker (internal/lease) for as long as it is
// actually working. So:
//
//   - a worker that is alive keeps renewing, and its row stays out of reach;
//   - a worker that dies — a deploy, a reboot, an OOM kill — stops renewing, and
//     the row's lease elapses.
//
// Sweep returns exactly the rows whose lease has elapsed. Nothing else.
//
// # Why this replaced the boot-time blanket requeue
//
// This package used to requeue EVERY row in 'running' once at start-up. That was
// safe only because of two assumptions: the process doing it was the
// deployment's only worker, and it had not started that worker yet. Under those
// conditions "everything running is dead" is a fact rather than a guess.
//
// Neither assumption survives a second instance. A node booting would have
// requeued jobs the other node was actively running, and the two would then
// duplicate the work — for transcode, two workers encoding one video into the
// same output prefix.
//
// A lease sweep needs no assumption about who else is alive, which is what makes
// it correct for one instance and for many. It is therefore no longer a
// start-up-only action: it runs periodically, so a worker that dies an hour into
// the day's uptime is recovered within a lease rather than at the next deploy.
//
// Without recovery a stranded row is permanent, and expensively so: the partial
// unique indexes that make enqueue idempotent (transcode_jobs_active_video_idx
// and friends) count a 'running' row as live, so re-enqueueing that video is a
// silent no-op; HasLiveTranscodeJob keeps answering true, so POST
// /admin/videos/:id/transcoding returns 409 replace_conflict forever; and
// peertube_import_runs_single_active_idx blocks EVERY future import run.
//
// Each queue is swept independently and a failure on one never stops the others:
// the alternative is that a single unrelated error leaves every other queue
// wedged, which is strictly worse than a partial recovery.
package jobrecovery

import (
	"context"
	"fmt"
)

// Queries is the subset of the generated query set Sweep needs. *sqlcgen.Queries
// satisfies it; tests substitute a fake.
type Queries interface {
	SweepExpiredTranscodeJobs(ctx context.Context) (int64, error)
	SweepExpiredImportJobs(ctx context.Context) (int64, error)
	SweepExpiredCaptionJobs(ctx context.Context) (int64, error)
	SweepExpiredAccountExports(ctx context.Context) (int64, error)
	SweepExpiredImportRuns(ctx context.Context) (int64, error)
	SweepExpiredChannelSyncs(ctx context.Context) (int64, error)
	SweepExpiredStorageMigrationObjects(ctx context.Context) (int64, error)
}

// Result is one queue's outcome. Requeued is the number of rows returned to the
// queue; Err is non-nil when that queue could not be swept (the others still
// were).
type Result struct {
	Queue    string
	Requeued int64
	Err      error
}

// Sweep returns every row whose lease has elapsed across all queues and reports
// what happened per queue, in a stable order so the log reads the same way every
// time. It never returns an error itself — recovery is best-effort by design and
// must not block start-up or a worker tick — so callers should log the per-queue
// results.
//
// It is safe to call concurrently from several instances: each sweep is a single
// UPDATE whose WHERE clause only matches rows nobody is renewing.
func Sweep(ctx context.Context, q Queries) []Result {
	steps := []struct {
		queue string
		run   func(context.Context) (int64, error)
	}{
		// Ordered by how visible the failure is to a user: a stuck transcode hides
		// a published video, a stuck import/caption hides its result, and a stuck
		// export/import-run/sync only blocks a repeat request. A stranded storage
		// migration object is invisible to users but blocks the campaign from ever
		// reaching 'synced', which is the gate on cutting over at all.
		{"transcode_jobs", q.SweepExpiredTranscodeJobs},
		{"import_jobs", q.SweepExpiredImportJobs},
		{"caption_jobs", q.SweepExpiredCaptionJobs},
		{"account_exports", q.SweepExpiredAccountExports},
		{"peertube_import_runs", q.SweepExpiredImportRuns},
		{"channel_syncs", q.SweepExpiredChannelSyncs},
		{"storage_migration_objects", q.SweepExpiredStorageMigrationObjects},
	}

	results := make([]Result, 0, len(steps))
	for _, s := range steps {
		n, err := s.run(ctx)
		if err != nil {
			err = fmt.Errorf("jobrecovery: sweep %s: %w", s.queue, err)
		}
		results = append(results, Result{Queue: s.queue, Requeued: n, Err: err})
	}
	return results
}
