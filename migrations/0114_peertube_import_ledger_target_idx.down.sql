-- Reverse 0114: drop the by-destination index. Nothing but query plans changes —
-- the lookup it serves still answers correctly, it just scans the ledger.
DROP INDEX IF EXISTS peertube_import_ledger_kind_target_done_idx;
