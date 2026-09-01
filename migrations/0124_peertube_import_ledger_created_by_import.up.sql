-- 0124: record on the ledger row whether the import CREATED the Vidra row it
-- points at, or merely LINKED to one that was already here.
--
-- The source-authoritative resync (0116) is defined as "it updates exactly the
-- rows the import owns and never touches anything created natively on Vidra",
-- and it answers "does the import own this?" with "is there a done ledger row
-- pointing at it?". Those are the same question only while every done row was
-- written by an INSERT — and they are not: --conflict-policy merge deliberately
-- maps a source account or channel onto one that ALREADY EXISTED here, and
-- records that mapping as done because the source's children have to resolve to
-- something. The two options compose into a run that treats a row this instance
-- made as the import's own and rewrites the source's values onto it.
--
-- Neither existing column can answer the question. vidra_id holds the same shape
-- either way, and 'done' is exactly what a merge link records. So the fact is
-- stated where it is known: ONCE, at creation. Every write that INSERTS takes
-- the default; the two link paths (users, channels) say FALSE explicitly, and
-- nothing re-asserts it on a later run.
--
-- DEFAULT TRUE keeps every row already in the table meaning what it means today,
-- so the column on its own changes no behaviour …
ALTER TABLE peertube_import_ledger
    ADD COLUMN created_by_import BOOLEAN NOT NULL DEFAULT TRUE;

-- … except for the rows that are PROVABLY links. Both notes below are written by
-- the merge path and by nothing else (an inserted row carries either no note or
-- a rename note), so an instance that has already run a merge import is repaired
-- rather than left exposed on its next resync. A row whose note an earlier
-- resync already overwrote cannot be recognised here — that is the limit of the
-- repair, not a claim of completeness.
UPDATE peertube_import_ledger
SET created_by_import = FALSE
WHERE (entity_kind = 'user'    AND note = 'merged into existing user')
   OR (entity_kind = 'channel' AND note = 'merged into existing channel');
