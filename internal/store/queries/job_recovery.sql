-- Boot-time job recovery (production-readiness step 5d).
--
-- Every durable queue in this schema claims work by flipping a row to 'running'
-- (or 'syncing') and only ever leaves that state from the worker that claimed
-- it. A deploy, a reboot or an OOM kill therefore strands the in-flight row in
-- 'running' FOREVER: nothing sweeps it, the partial unique indexes
-- (transcode_jobs_active_video_idx, import_jobs_active_video_idx,
-- account_exports_active_user_idx, peertube_import_runs_single_active_idx) count
-- it as live so a re-enqueue is a silent no-op, and HasLiveTranscodeJob keeps
-- answering true so the admin re-transcode endpoint 409s permanently.
--
-- These statements are run ONCE at api start-up, before any worker goroutine
-- exists (see internal/jobrecovery), which is what makes the blanket
-- "everything running is dead" assumption safe: this process is the only worker,
-- and it has not started yet. A MULTI-NODE deployment must not run them — a
-- second node booting would steal jobs actively running on the first. The
-- durable fix for that is per-row leases (see media_ipfs_pins for the pattern).
--
-- attempts is incremented so recovery cannot loop forever: a genuinely poisonous
-- job that crashes the process on every boot walks its attempt counter up and
-- dead-letters through the workers' normal maxAttempts path instead of taking
-- the instance down repeatedly. next_attempt_at is deliberately left alone —
-- the row was already due when it was claimed, so it is picked up on the first
-- tick.

-- name: RequeueRunningTranscodeJobs :execrows
UPDATE transcode_jobs
SET state = 'pending', attempts = attempts + 1, updated_at = now()
WHERE state = 'running';

-- name: RequeueRunningImportJobs :execrows
-- stage is coarse in-flight progress ('downloading', 'processing', ...) and is
-- meaningless for a row that is back in the queue, so it resets with the state.
UPDATE import_jobs
SET state = 'pending', attempts = attempts + 1, stage = '', updated_at = now()
WHERE state = 'running';

-- name: RequeueRunningCaptionJobs :execrows
-- Same reset as import_jobs, plus the percentage the claim query seeds (5).
UPDATE caption_jobs
SET state = 'pending', attempts = attempts + 1, stage = '', progress_percent = 0, updated_at = now()
WHERE state = 'running';

-- name: RequeueRunningAccountExports :execrows
UPDATE account_exports
SET state = 'pending', attempts = attempts + 1, updated_at = now()
WHERE state = 'running';

-- name: RequeueRunningPeerTubeImportRuns :execrows
-- started_at is cleared so the admin history does not show a run "started" at a
-- time that belongs to a previous, dead process.
UPDATE peertube_import_runs
SET state = 'pending', attempts = attempts + 1, started_at = NULL, updated_at = now()
WHERE state = 'running';

-- name: RequeueSyncingChannelSyncs :execrows
-- channel_syncs has no attempt counter and no dead-lettering: a sync that keeps
-- failing simply retries on its next cadence, so 'idle' (not 'failed') is the
-- honest resting state for one that was interrupted rather than rejected.
-- next_run_at is untouched, so an interrupted sync is due immediately.
UPDATE channel_syncs
SET state = 'idle', updated_at = now()
WHERE state = 'syncing';
