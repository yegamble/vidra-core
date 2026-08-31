-- 0122 down: drop the retention-prune index. The prune query still works
-- without it (it degrades to a scan), so this is safe to reverse.
DROP INDEX IF EXISTS search_outbox_prune_idx;
