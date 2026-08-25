-- 0114: let the import look its own ledger up by the DESTINATION row it wrote,
-- not only by the source id it is keyed on.
--
-- The actor-image pass needs one answer the (entity_kind, source_id) key cannot
-- give: "is the avatar in this slot one I wrote?". PeerTube keeps several
-- actorImage rows per avatar and mints a new one whenever the picture changes,
-- so the ledger row describing the slot's current occupant is a different source
-- id from the one a later run is deciding about. The lookup therefore runs on
-- vidra_id, which follows the SLOT instead of the file.
--
-- Without this index that lookup is a sequential scan of the whole ledger, once
-- per slot — 155k rows x 309 avatars on the migration that motivated it. The
-- partial predicate keeps the index to the rows the lookup can return
-- (completed writes), which is a small fraction of a ledger dominated by
-- 'skipped'.
--
-- Additive: a new index, no column or constraint changed.
CREATE INDEX IF NOT EXISTS peertube_import_ledger_kind_target_done_idx
    ON peertube_import_ledger (entity_kind, vidra_id)
    WHERE status = 'done';
