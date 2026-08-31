-- 0122: index the search_outbox retention prune (PruneSearchOutbox).
--
-- 0092 created the outbox with exactly one index -- search_outbox_due_idx,
-- partial on state = 'pending' -- because delivery was the only access path.
-- Nothing ever deleted from this table, so every server-side search.submitted
-- (raw query text + user_id + session_id), every forwarded behavioural event and
-- every reconcile.page (up to 200 whole documents of JSONB, on a 24h loop) has
-- accumulated in the PRIMARY database forever. That is a privacy defect before
-- it is a capacity one: those rows outlive both "Clear search history" and
-- account deletion.
--
-- The prune walks the rows the due index deliberately excludes, so it needs its
-- own. The predicate is the exact complement of the due index:
--
--   * state is leading and used with EQUALITY (the prune runs once per prunable
--     state, so the two windows can differ), created_at gives the range and the
--     ORDER BY, id breaks ties -- which makes one batch an index range scan that
--     stops at LIMIT instead of a seq scan plus a top-N sort of the whole table
--     on every pass. On a table holding a retention window's worth of rows,
--     that is the difference between a bounded sweep and a scan per tick that
--     finds nothing.
--   * `WHERE state <> 'pending'` keeps the index off the hot path entirely:
--     every INSERT is pending, so an enqueue never touches this index. A row
--     enters it once, when the drainer marks it delivered or dead, and leaves
--     it when the prune deletes it.
CREATE INDEX search_outbox_prune_idx
    ON search_outbox (state, created_at, id)
    WHERE state <> 'pending';
