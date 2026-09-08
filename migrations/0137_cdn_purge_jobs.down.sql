-- Reverse 0137. Dropping the queue loses PENDING INVALIDATIONS: any retry that
-- had not yet succeeded, an account deletion's snapshot, and an unfinished
-- downloads-revocation walk. The rows it drops are intent, not data — the
-- deletions and flips they follow have already committed — so the cost of the
-- rollback is that an edge keeps serving those objects until its own TTL, which
-- is exactly the state the release before this one was in.
DROP INDEX IF EXISTS cdn_purge_jobs_active_walk_idx;
DROP INDEX IF EXISTS cdn_purge_jobs_state_created_idx;
DROP INDEX IF EXISTS cdn_purge_jobs_due_idx;
DROP TABLE IF EXISTS cdn_purge_jobs;
