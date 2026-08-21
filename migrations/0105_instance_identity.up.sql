-- 0105: Instance identity — the UUID that says which install this database is.
--
-- It exists for the media-GC bucket-ownership marker (phase-2 storage, work
-- item 1). Media garbage collection deletes every stored object no database row
-- references, which is only a safe inference when the database and the object
-- store belong to the SAME install. Vidra therefore stamps this UUID into the
-- store at `.vidra/owner`, and a sweep that finds someone else's UUID there —
-- or finds a store full of objects with no marker at all — refuses to delete
-- and reports a dry run instead.
--
-- One row, inserted here, never updated: the identity has to survive every
-- restart and every redeploy, because a value that changed would make the
-- install disown its own bucket. It is a dump-and-restore-stable identifier and
-- is not a secret — it is written in plain text into the operator's own bucket.
CREATE TABLE instance_identity (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The one row. A restored dump carries its own identity forward, which is the
-- point: the restored install still owns the bucket it owned before.
INSERT INTO instance_identity DEFAULT VALUES;
