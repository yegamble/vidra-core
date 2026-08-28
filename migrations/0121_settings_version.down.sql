-- Reverse 0121: drop the cross-replica invalidation counter. Nothing else
-- references it, so no data is lost — but a multi-replica deployment rolled
-- back to here goes back to the 1-of-N staleness this table exists to close:
-- an admin change takes effect only on the replica that served the write,
-- until every other replica is restarted.
DROP TABLE IF EXISTS settings_version;
