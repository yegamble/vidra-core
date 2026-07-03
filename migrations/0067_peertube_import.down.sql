-- Reverse 0067: drop the PeerTube import bookkeeping tables. This removes the
-- idempotency ledger and run history only; it does not touch anything that was
-- imported into the core tables (users/channels/videos/...), which are owned by
-- their own migrations.
DROP TABLE IF EXISTS peertube_import_runs;
DROP TABLE IF EXISTS peertube_import_ledger;
