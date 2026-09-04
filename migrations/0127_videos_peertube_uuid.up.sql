-- 0127: remember, on the video itself, which PeerTube video it was imported
-- from — so an imported instance's old public links keep resolving.
--
-- THE PROBLEM. PeerTube's public watch URLs are /w/{shortUUID} and the legacy
-- /videos/watch/{uuid}, both naming the SOURCE instance's video UUID. The
-- importer does not preserve that UUID: ImportInsertVideo omits `id`, so
-- Postgres mints a fresh one. After an operator points their domain at Vidra,
-- every link anyone ever shared to that instance is unresolvable, because
-- nothing on the video says which source video it is.
--
-- WHY NOT JUST READ THE IMPORT LEDGER. peertube_import_ledger already holds
-- (entity_kind='video', source_id=<PeerTube UUID>) -> vidra_id, and that is
-- where this backfill gets its data. But it is import BOOKKEEPING, and two
-- import code paths are licensed to rewrite it: resolveParent retires a mapping
-- to status='skipped', vidra_id=NULL when the row it points at is gone, and
-- UpsertImportLedgerEntry repoints vidra_id on conflict. It deliberately carries
-- no foreign key to videos (see the rationale on ImportParentStillExists), so
-- between runs it is EXPECTED to hold dangling pointers. Several entity kinds
-- also share the same source_id for one video. A public URL must not resolve
-- through a table with those semantics — and its down migration drops the whole
-- table, which would take the URLs with it.
--
-- WHY THE UUID AND NOT THE shortUUID STRING. PeerTube has no shortUUID column
-- either; it computes one in application code with the `short-uuid` package
-- (flickrBase58, padded to exactly 22 characters). Storing the UUID keeps the
-- ENCODING a decode-time concern: if the decoder is ever found wrong, it is a
-- code fix, not a data migration. Note the alphabet is NOT the one
-- internal/shortid uses — same 58 characters, different ORDER — so a PeerTube
-- shortUUID decoded with shortid.ToUUID yields a different, wrong UUID rather
-- than an error. The two forms must never share a route.
ALTER TABLE videos ADD COLUMN peertube_uuid UUID;

-- Backfill from the ledger for everything already imported, in the same file
-- that adds the column (the 0124 precedent). status='done' is the only state
-- whose vidra_id is a live result — and it is served exactly by
-- peertube_import_ledger_kind_target_done_idx. The source_id shape guard keeps a
-- malformed bookkeeping row from failing the whole migration on ::uuid; a row
-- that cannot be parsed is simply not backfilled, which is the same outcome as
-- never having been imported.
UPDATE videos v
SET peertube_uuid = l.source_id::uuid
FROM peertube_import_ledger l
WHERE l.entity_kind = 'video'
  AND l.status = 'done'
  AND l.vidra_id = v.id
  AND l.source_id ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$';

-- PARTIAL UNIQUE, and the uniqueness is the point. With a plain index two videos
-- could claim one legacy URL, and the resolver's LIMIT 1 would then send half of
-- that URL's traffic to the wrong video — silently, which is the worst failure
-- available here. Unique instead makes a duplicate import of one source video a
-- LOUD per-video failure the importer records and continues past.
--
-- It cannot fail on the backfill above: the ledger's own UNIQUE (entity_kind,
-- source_id) guarantees each source UUID appears once, so each value is assigned
-- to at most one video.
CREATE UNIQUE INDEX videos_peertube_uuid_key ON videos (peertube_uuid)
    WHERE peertube_uuid IS NOT NULL;
