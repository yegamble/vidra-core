-- 0123 down: drop the per-user erasure index. PurgeUserSearchOutbox still
-- returns the same rows without it (it degrades to a sequential scan), so this
-- is safe to reverse -- it costs latency, never correctness.
DROP INDEX IF EXISTS search_outbox_user_purge_idx;
