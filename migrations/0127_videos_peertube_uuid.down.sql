-- Reverse 0127: drop the imported-source UUID.
--
-- The values are re-derivable: re-applying 0127 rebuilds them from the same
-- peertube_import_ledger rows this migration read, so nothing is permanently
-- lost while the ledger survives. What breaks meanwhile is legacy-link
-- resolution — /w/{shortUUID} and /videos/watch/{uuid} for imported videos 404
-- until the column is back.
DROP INDEX IF EXISTS videos_peertube_uuid_key;

ALTER TABLE videos DROP COLUMN peertube_uuid;
