-- 0110: partial indexes for the LEASE SWEEP scan on every swept durable queue.
--
-- Each queue already carries a `<table>_due_idx` partial index for the CLAIM
-- scan — `(next_attempt_at) WHERE state = 'pending'`. Nothing covered the other
-- half of the same column's job: the sweep, which reads the identical column but
-- over the CLAIMED half of the table ('running'; 'syncing' for channel_syncs,
-- 'copying' for storage_migration_objects). The claim index cannot serve it — a
-- partial index only answers queries whose predicate implies its own, and
-- 'running' does not imply 'pending'.
--
-- What the planner fell back to varied by table, and none of the fallbacks could
-- seek to "leases that have actually elapsed":
--
--   * transcode_jobs, import_jobs, caption_jobs, account_exports and
--     peertube_import_runs have a partial UNIQUE index over live rows
--     (transcode_jobs_active_video_idx and friends, WHERE state IN
--     ('pending','running')). The planner bitmap-scans that index IN FULL and
--     then filters, so each sweep costs the whole in-flight backlog.
--   * channel_syncs has nothing at all: channel_syncs_due_idx deliberately
--     covers only ('waiting_first_run','idle','failed'), so sweeping 'syncing'
--     was a sequential scan of the table (measured: 20k rows → Seq Scan, cost
--     695, to find 3 expired leases).
--   * storage_migration_objects could only use (campaign_id, state), which the
--     sweep has no campaign to lead with — a full index scan at best.
--
-- In every case the work per sweep is proportional to the backlog (or the whole
-- table) rather than to the number of rows that actually need recovering, which
-- in a healthy install is zero. On a single-worker install that is invisible.
-- On a worker FLEET it is the exact opposite shape of work you want: every
-- instance paying that cost seven times, every two minutes, forever. These
-- indexes turn each sweep into a range scan over the (usually empty) set of rows
-- whose lease has actually elapsed.
--
-- They also back the LIMIT + FOR UPDATE SKIP LOCKED claim that the SweepExpired*
-- statements now use: the subquery orders by the lease column, which is this
-- index's leading (and only) key.
--
-- Naming follows the existing `<table>_due_idx` convention with the state it
-- covers: `<table>_lease_sweep_idx`.

CREATE INDEX IF NOT EXISTS transcode_jobs_lease_sweep_idx
    ON transcode_jobs (next_attempt_at)
    WHERE state = 'running';

CREATE INDEX IF NOT EXISTS import_jobs_lease_sweep_idx
    ON import_jobs (next_attempt_at)
    WHERE state = 'running';

CREATE INDEX IF NOT EXISTS caption_jobs_lease_sweep_idx
    ON caption_jobs (next_attempt_at)
    WHERE state = 'running';

CREATE INDEX IF NOT EXISTS account_exports_lease_sweep_idx
    ON account_exports (next_attempt_at)
    WHERE state = 'running';

CREATE INDEX IF NOT EXISTS peertube_import_runs_lease_sweep_idx
    ON peertube_import_runs (next_attempt_at)
    WHERE state = 'running';

-- channel_syncs is the odd one out in BOTH columns: its lease lives in
-- next_run_at (the row is a recurring schedule, not a one-shot job) and its
-- claimed state is 'syncing'.
CREATE INDEX IF NOT EXISTS channel_syncs_lease_sweep_idx
    ON channel_syncs (next_run_at)
    WHERE state = 'syncing';

-- storage_migration_objects claims into 'copying'. The existing
-- storage_migration_objects_campaign_state_idx (campaign_id, state) cannot serve
-- the sweep: the sweep has no campaign to lead with, so that index is only ever
-- a full-index scan for this query.
CREATE INDEX IF NOT EXISTS storage_migration_objects_lease_sweep_idx
    ON storage_migration_objects (next_attempt_at)
    WHERE state = 'copying';
