-- Reverse 0124: drop the provenance flag. The ledger rows themselves stay; only
-- the record of which of them point at a row this instance made is lost, so a
-- source-authoritative run after a re-apply of 0124 again treats a merge-linked
-- account or channel as the import's own to rewrite.
ALTER TABLE peertube_import_ledger
    DROP COLUMN IF EXISTS created_by_import;
