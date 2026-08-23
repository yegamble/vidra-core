-- Rollback 0110: drop the lease-sweep partial indexes. The sweeps stay correct
-- without them (they only lose the index and fall back to a sequential scan).
DROP INDEX IF EXISTS storage_migration_objects_lease_sweep_idx;
DROP INDEX IF EXISTS channel_syncs_lease_sweep_idx;
DROP INDEX IF EXISTS peertube_import_runs_lease_sweep_idx;
DROP INDEX IF EXISTS account_exports_lease_sweep_idx;
DROP INDEX IF EXISTS caption_jobs_lease_sweep_idx;
DROP INDEX IF EXISTS import_jobs_lease_sweep_idx;
DROP INDEX IF EXISTS transcode_jobs_lease_sweep_idx;
