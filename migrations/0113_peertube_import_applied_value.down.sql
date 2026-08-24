-- Reverse 0113: drop the single-value memory. The rows it annotated stay; only
-- the record of what the import last applied is lost, so the next import reads
-- an operator-owned taxonomy where it would previously have recognised its own
-- write — it stops updating that setting rather than overwriting it, which is
-- the safe direction to fail in.
ALTER TABLE peertube_import_ledger
    DROP COLUMN IF EXISTS applied_value;
