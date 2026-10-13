-- Blocklist subscriptions (migration 0176): shared denylists synced into
-- blocked_instances. A subscription owns exactly the rows whose
-- blocked_instances.source = 'subscription:<id>'.

-- name: CreateBlocklistSubscription :one
INSERT INTO blocklist_subscriptions (url, label, format, enabled, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetBlocklistSubscription :one
SELECT * FROM blocklist_subscriptions WHERE id = $1;

-- name: ListBlocklistSubscriptions :many
-- block_count is how many blocks this subscription currently owns.
SELECT s.*,
       (SELECT count(*) FROM blocked_instances b WHERE b.source = 'subscription:' || s.id::text)::bigint AS block_count
FROM blocklist_subscriptions s
ORDER BY s.created_at DESC, s.id
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountBlocklistSubscriptions :one
SELECT count(*)::bigint FROM blocklist_subscriptions;

-- name: UpdateBlocklistSubscription :one
UPDATE blocklist_subscriptions SET label = $2, enabled = $3 WHERE id = $1
RETURNING *;

-- name: DeleteBlocklistSubscription :execrows
DELETE FROM blocklist_subscriptions WHERE id = $1;

-- name: ListDueBlocklistSubscriptions :many
-- Enabled subscriptions not attempted since the cutoff (never-attempted first).
SELECT * FROM blocklist_subscriptions
WHERE enabled AND (last_attempt_at IS NULL OR last_attempt_at < sqlc.arg('cutoff')::timestamptz)
ORDER BY last_attempt_at NULLS FIRST, id
LIMIT sqlc.arg('result_limit');

-- name: RecordBlocklistSyncResult :exec
-- The validator, counts and synced_at move only on a fully applied sync (ok) or a
-- 304; a failed attempt leaves the last good state visible beside its error.
UPDATE blocklist_subscriptions SET
    last_attempt_at = now(),
    last_status     = sqlc.arg('status')::text,
    last_error      = sqlc.arg('error')::text,
    last_synced_at  = CASE WHEN sqlc.arg('status')::text IN ('ok', 'not_modified') THEN now() ELSE last_synced_at END,
    etag            = CASE WHEN sqlc.arg('status')::text = 'ok' THEN sqlc.arg('etag')::text ELSE etag END,
    last_added      = CASE WHEN sqlc.arg('status')::text = 'ok' THEN sqlc.arg('added')::int ELSE last_added END,
    last_removed    = CASE WHEN sqlc.arg('status')::text = 'ok' THEN sqlc.arg('removed')::int ELSE last_removed END,
    entry_count     = CASE WHEN sqlc.arg('status')::text = 'ok' THEN sqlc.arg('entries')::int ELSE entry_count END
WHERE id = sqlc.arg('id');

-- name: InsertSubscriptionBlocks :many
-- Add the domains as blocks owned by the subscription. ON CONFLICT DO NOTHING is
-- the manual-wins rule: a domain that is already blocked (by an admin, or by
-- another subscription) is left exactly as it is. The EXISTS guards close two
-- races: a subscription deleted mid-sync cannot be left owning rows, and a
-- domain allowlisted mid-sync is not blocked.
INSERT INTO blocked_instances (domain, reason, source)
SELECT t.d, sqlc.arg('reason')::text, 'subscription:' || sqlc.arg('subscription_id')::uuid::text
FROM unnest(sqlc.arg('domains')::text[]) AS t(d)
WHERE EXISTS (SELECT 1 FROM blocklist_subscriptions WHERE id = sqlc.arg('subscription_id')::uuid)
  AND NOT EXISTS (SELECT 1 FROM blocklist_allowlist a WHERE a.domain = t.d)
ON CONFLICT (domain) DO NOTHING
RETURNING domain;

-- name: DeleteSubscriptionBlocksNotIn :many
-- Remove the blocks THIS subscription owns that its list no longer asks for.
-- Never matches a manual row or another subscription's. created_at comes back so
-- the caller can resume deliveries the block cancelled.
DELETE FROM blocked_instances
WHERE source = sqlc.arg('source')::text
  AND NOT (domain = ANY(sqlc.arg('keep')::text[]))
RETURNING domain, created_at;

-- name: DeleteAllSubscriptionBlocksForSource :many
-- Subscription removal: drop every block it owns, nothing else.
DELETE FROM blocked_instances WHERE source = sqlc.arg('source')::text
RETURNING domain, created_at;

-- name: DeleteSubscriptionBlocksForDomain :many
-- Allowlisting a domain drops the subscription-owned block now (a manual block
-- is untouched).
DELETE FROM blocked_instances WHERE domain = $1 AND source <> 'manual'
RETURNING domain, created_at;

-- name: AddBlocklistAllow :execrows
INSERT INTO blocklist_allowlist (domain, added_by) VALUES ($1, $2)
ON CONFLICT (domain) DO NOTHING;

-- name: RemoveBlocklistAllow :execrows
DELETE FROM blocklist_allowlist WHERE domain = $1;

-- name: ListBlocklistAllow :many
SELECT domain, added_by, created_at FROM blocklist_allowlist
ORDER BY created_at DESC, domain
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountBlocklistAllow :one
SELECT count(*)::bigint FROM blocklist_allowlist;

-- name: ListAllBlocklistAllowDomains :many
SELECT domain FROM blocklist_allowlist;
