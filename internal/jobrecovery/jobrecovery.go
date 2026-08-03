// Package jobrecovery returns jobs stranded mid-flight by an unclean shutdown
// to their queues, once, at process start-up.
//
// Every durable queue in vidra-core claims work by flipping a row to 'running'
// (channel_syncs: 'syncing') and only ever leaves that state from the worker
// goroutine that claimed it. There is no lease and no expiry, so a deploy, a
// reboot or an OOM kill strands the in-flight row permanently:
//
//   - the partial unique indexes that make enqueue idempotent
//     (transcode_jobs_active_video_idx and friends) count a 'running' row as
//     live, so re-enqueueing that video/user is a silent no-op;
//   - HasLiveTranscodeJob keeps answering true, so POST
//     /admin/videos/:id/transcoding returns 409 replace_conflict forever;
//   - peertube_import_runs_single_active_idx blocks EVERY future import run.
//
// During launch week the stack is redeployed repeatedly, so this bites on the
// first deploy that lands while a transcode is running.
//
// # Why a blanket requeue is safe here, and where it is not
//
// Recover must run BEFORE any worker goroutine is started, in a process that is
// the instance's only worker. Under those two conditions "every row in 'running'
// is dead" is not a heuristic — nothing else can be holding one. Both conditions
// are true for the single-node deployment this ships as (cmd/api starts every
// worker in-process).
//
// They are NOT true of a multi-node deployment: a second api node booting would
// requeue jobs the first node is actively running, and the two would then
// duplicate the work. Before scaling out, replace this with per-row leases
// (claimed_at / claimed_by with an expiry sweep — migration 0071's
// media_ipfs_pins is the in-repo pattern), which recovers stranded work without
// needing to assume anything about who else is alive.
//
// Each queue is recovered independently and a failure on one never stops the
// others: the alternative is that a single unrelated error leaves every other
// queue wedged for the life of the process, which is strictly worse than a
// partial recovery.
package jobrecovery

import (
	"context"
	"fmt"
)

// Queries is the subset of the generated query set Recover needs. *sqlcgen.Queries
// satisfies it; tests substitute a fake.
type Queries interface {
	RequeueRunningTranscodeJobs(ctx context.Context) (int64, error)
	RequeueRunningImportJobs(ctx context.Context) (int64, error)
	RequeueRunningCaptionJobs(ctx context.Context) (int64, error)
	RequeueRunningAccountExports(ctx context.Context) (int64, error)
	RequeueRunningPeerTubeImportRuns(ctx context.Context) (int64, error)
	RequeueSyncingChannelSyncs(ctx context.Context) (int64, error)
}

// Result is one queue's outcome. Requeued is the number of rows returned to the
// queue; Err is non-nil when that queue could not be recovered (the others still
// were).
type Result struct {
	Queue    string
	Requeued int64
	Err      error
}

// Recover requeues every stranded row across all queues and reports what
// happened per queue, in a stable order so the boot log reads the same way every
// time. It never returns an error itself — recovery is best-effort by design and
// must not block start-up — so callers should log the per-queue results.
func Recover(ctx context.Context, q Queries) []Result {
	steps := []struct {
		queue string
		run   func(context.Context) (int64, error)
	}{
		// Ordered by how visible the failure is to a user: a stuck transcode hides
		// a published video, a stuck import/caption hides its result, and a stuck
		// export/import-run/sync only blocks a repeat request.
		{"transcode_jobs", q.RequeueRunningTranscodeJobs},
		{"import_jobs", q.RequeueRunningImportJobs},
		{"caption_jobs", q.RequeueRunningCaptionJobs},
		{"account_exports", q.RequeueRunningAccountExports},
		{"peertube_import_runs", q.RequeueRunningPeerTubeImportRuns},
		{"channel_syncs", q.RequeueSyncingChannelSyncs},
	}

	results := make([]Result, 0, len(steps))
	for _, s := range steps {
		n, err := s.run(ctx)
		if err != nil {
			err = fmt.Errorf("jobrecovery: requeue %s: %w", s.queue, err)
		}
		results = append(results, Result{Queue: s.queue, Requeued: n, Err: err})
	}
	return results
}
