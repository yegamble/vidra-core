-- Reverse 0112: drop the counter memory. The rows it annotated stay; only the
-- record of how much of each source counter was already applied is lost, so the
-- next import after a re-apply of 0112 treats every counter as unapplied and
-- adds the full source total again.
ALTER TABLE peertube_import_ledger
    DROP COLUMN IF EXISTS source_value;
