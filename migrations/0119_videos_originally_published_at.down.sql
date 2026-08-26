-- Reverse 0119: drop the original-publication date.
--
-- The dates themselves are lost, but they are all re-derivable: they only ever
-- come from the source instance (the PeerTube importer's backfill pass re-reads
-- and re-applies them on the next run) or from a creator's own edit.
ALTER TABLE videos DROP COLUMN originally_published_at;
