-- Storage migration campaigns + the per-object location record (migration 0107,
-- phase-2 storage work items 4 and 5).
--
-- The object ledger is a durable queue born on the convention every NEW queue
-- must use (interfaces.md section 7): FOR UPDATE SKIP LOCKED claims, a
-- lease-shaped next_attempt_at that the worker renews while it copies, and
-- terminal writes guarded on the state the claim put the row in. That is what
-- makes the copy worker safe to run UNLEADERED on every instance -- more
-- instances simply copy faster.
--
-- Object keys are never rewritten by a move, so nothing here touches
-- media_ipfs_pins (whose primary key IS a storage key) or video_files.storage_key.

-- name: CreateStorageMigration :one
-- Start a campaign. The partial unique index storage_migrations_single_active_idx
-- makes a second live campaign a constraint violation rather than a race.
INSERT INTO storage_migrations (source_desc, target_desc)
VALUES ($1, $2)
RETURNING *;

-- name: GetStorageMigration :one
SELECT * FROM storage_migrations WHERE id = $1;

-- name: GetActiveStorageMigration :one
-- The one live campaign, if there is one. LIMIT 1 is belt-and-braces: the
-- partial unique index already guarantees at most one row matches.
SELECT * FROM storage_migrations
WHERE state NOT IN ('done', 'cancelled', 'failed')
ORDER BY created_at DESC, id
LIMIT 1;

-- name: ListStorageMigrations :many
SELECT * FROM storage_migrations ORDER BY created_at DESC, id LIMIT $1;

-- name: HasActiveStorageMigration :one
-- The media-GC interlock. Deliberately WIDER than "live": a 'failed' campaign
-- still counts, because a move that stopped half-way is exactly the state in
-- which "no database row references this object" stops being evidence about
-- either store. Only an operator resolving it (cancel, or a completed run) lets
-- destructive sweeps resume.
SELECT EXISTS (
    SELECT 1 FROM storage_migrations WHERE state NOT IN ('done', 'cancelled')
);

-- name: SetStorageMigrationState :exec
-- Advance a live campaign. Guarded on the terminal set so a cancel that lands
-- between a sweep's read and its write is never overwritten.
UPDATE storage_migrations
SET state = $2, last_error = $3, updated_at = now()
WHERE id = $1 AND state NOT IN ('done', 'cancelled', 'failed');

-- name: SetStorageMigrationError :exec
-- Record a diagnostic WITHOUT changing state (the campaign is stuck, not over).
-- The IS DISTINCT FROM guard keeps a repeating sweep from touching the row every
-- tick, which would churn the job_runs projection for no new information.
UPDATE storage_migrations
SET last_error = $2, updated_at = now()
WHERE id = $1 AND state NOT IN ('done', 'cancelled', 'failed') AND last_error IS DISTINCT FROM $2;

-- name: MarkStorageMigrationCutover :exec
-- Record that the primary backend is now serving from the campaign's TARGET,
-- i.e. the operator's env swap took effect. observed_cutover_at is written once
-- and never moved: the grace period before the source is deleted is measured
-- from the first observation, so a restart cannot silently restart the clock.
UPDATE storage_migrations
SET state = 'cutover',
    observed_cutover_at = COALESCE(observed_cutover_at, now()),
    last_error = '',
    updated_at = now()
WHERE id = $1 AND state IN ('copying', 'synced');

-- name: CancelStorageMigration :execrows
-- Operator abort. Object rows stop being claimed immediately: the claim query
-- joins the campaign and only takes rows from a 'copying'/'synced' one.
UPDATE storage_migrations
SET state = 'cancelled', updated_at = now()
WHERE id = $1 AND state NOT IN ('done', 'cancelled', 'failed');

-- name: RefreshStorageMigrationCounters :exec
-- Recompute the campaign counters FROM the object ledger. Derived rather than
-- incremented on purpose: an optimistic counter drifts the moment a worker dies
-- mid-batch, and this one feeds a progress percentage an operator makes a
-- delete-the-source decision on. The IS DISTINCT FROM guard means a tick that
-- found no change does not touch the row at all.
UPDATE storage_migrations m
SET objects_total  = c.total,
    objects_done   = c.done,
    objects_failed = c.failed,
    updated_at     = now()
FROM (
    SELECT
        count(*)::bigint AS total,
        count(*) FILTER (WHERE state IN ('verified', 'source_deleted'))::bigint AS done,
        count(*) FILTER (WHERE state = 'failed')::bigint AS failed
    FROM storage_migration_objects
    WHERE campaign_id = $1
) c
WHERE m.id = $1
  AND (m.objects_total, m.objects_done, m.objects_failed)
      IS DISTINCT FROM (c.total, c.done, c.failed);

-- name: UpsertStorageMigrationObjects :execrows
-- Record a batch of enumerated keys. DO NOTHING on conflict is what makes the
-- delta pass cheap and idempotent: a re-enumeration re-offers every key it saw
-- last time and only genuinely new objects become rows, so the rowcount IS the
-- answer to "did anything appear since?".
INSERT INTO storage_migration_objects (object_key, campaign_id)
SELECT k, sqlc.arg(campaign_id)
FROM unnest(sqlc.arg(object_keys)::text[]) AS k
ON CONFLICT (object_key) DO NOTHING;

-- name: ClaimDueStorageMigrationObjects :many
-- Atomically claim due objects for copying, oldest first, by flipping them to
-- 'copying' and pushing the lease 30 minutes out.
--
-- FOR UPDATE OF o SKIP LOCKED is what makes this safe on every instance at once:
-- concurrent claimers take disjoint rows instead of racing to re-evaluate the
-- subquery. Only the object rows are locked -- the campaign join is a read, and
-- locking it would serialise every worker behind one row.
--
-- The ORDER BY is TOTAL (next_attempt_at, object_key): a partial order lets
-- Postgres return a different arbitrary tie-break per claimer, which turns
-- "oldest first" into "whatever the plan felt like" under contention.
UPDATE storage_migration_objects
SET next_attempt_at = now() + interval '30 minutes',
    state = 'copying', updated_at = now()
WHERE object_key IN (
    SELECT o.object_key
    FROM storage_migration_objects o
    JOIN storage_migrations m ON m.id = o.campaign_id
    WHERE o.state = 'pending'
      AND o.next_attempt_at <= now()
      AND m.state IN ('copying', 'synced')
    ORDER BY o.next_attempt_at, o.object_key
    LIMIT $1
    FOR UPDATE OF o SKIP LOCKED
)
RETURNING object_key, campaign_id, attempts;

-- name: RenewStorageMigrationObjectLease :exec
-- Push a claimed object's lease forward while the copy is still streaming. A
-- whole video original over a slow link can outlive one lease; without this the
-- sweep would hand it to a second worker mid-copy. Guarded on 'copying' so a row
-- that already finished cannot be revived.
UPDATE storage_migration_objects
SET next_attempt_at = now() + interval '30 minutes'
WHERE object_key = $1 AND state = 'copying';

-- name: SweepExpiredStorageMigrationObjects :execrows
-- Return objects whose lease elapsed while they were 'copying' to the queue.
-- attempts is incremented so an object that kills its worker every time walks
-- its counter up and dead-letters through the normal path.
UPDATE storage_migration_objects
SET state = 'pending', attempts = attempts + 1, updated_at = now()
WHERE state = 'copying' AND next_attempt_at <= now();

-- name: MarkStorageMigrationObjectVerified :exec
-- The object is in the target AND was read back from it and re-hashed. This is
-- the per-object location record flipping (interfaces.md section 3): from here
-- on, dual-read and delete-source both treat the target as holding these bytes.
UPDATE storage_migration_objects
SET state = 'verified', sha256 = $2, byte_size = $3, last_error = '', updated_at = now()
WHERE object_key = $1 AND state = 'copying';

-- name: RescheduleStorageMigrationObject :exec
-- Transient failure: back to pending with backoff, one attempt spent.
UPDATE storage_migration_objects
SET state = 'pending', attempts = attempts + 1, next_attempt_at = $2, last_error = $3, updated_at = now()
WHERE object_key = $1 AND state = 'copying';

-- name: FailStorageMigrationObject :exec
-- Dead-letter: no further retries. last_error is a CATEGORY, never a key or a
-- raw backend error -- it is projected into the admin jobs overview.
UPDATE storage_migration_objects
SET state = 'failed', attempts = attempts + 1, last_error = $2, updated_at = now()
WHERE object_key = $1 AND state = 'copying';

-- name: MarkStorageMigrationObjectSourceDeleted :exec
-- The verified copy's SOURCE object has been removed. Guarded on 'verified' so
-- nothing can record a source deletion for an object that was never proven to
-- have arrived.
UPDATE storage_migration_objects
SET state = 'source_deleted', updated_at = now()
WHERE object_key = $1 AND state = 'verified';

-- name: ListVerifiedStorageMigrationObjects :many
-- The delete-source batch: objects proven to be in the target whose source copy
-- is still there.
SELECT object_key FROM storage_migration_objects
WHERE campaign_id = $1 AND state = 'verified'
ORDER BY object_key
LIMIT $2;

-- name: CountUncopiedStorageMigrationObjects :one
-- Objects not yet proven to be in the target. Zero is the precondition for
-- declaring a campaign synced, and for ever deleting anything from the source.
SELECT count(*)::bigint AS count FROM storage_migration_objects
WHERE campaign_id = $1 AND state IN ('pending', 'copying');

-- name: CountUnfinishedStorageMigrationObjects :one
-- Objects whose source copy still exists (or that never arrived). Zero ends a
-- campaign; 'failed' rows deliberately do not count, because a dead-lettered
-- object is a reported fact, not unfinished work.
SELECT count(*)::bigint AS count FROM storage_migration_objects
WHERE campaign_id = $1 AND state IN ('pending', 'copying', 'verified');

-- name: CountStorageMigrationObjectsByState :many
-- The per-state breakdown shown on one campaign.
SELECT state, count(*)::bigint AS count
FROM storage_migration_objects
WHERE campaign_id = $1
GROUP BY state
ORDER BY state;

-- name: StorageMigrationStats :one
-- Admin jobs overview card. The unit is the OBJECT, because that is the work:
-- one campaign row would show a single job that is 'running' for a week.
SELECT
    count(*) FILTER (WHERE state = 'pending')::bigint AS pending,
    count(*) FILTER (WHERE state = 'copying')::bigint AS running,
    count(*) FILTER (WHERE state IN ('verified', 'source_deleted'))::bigint AS done,
    count(*) FILTER (WHERE state = 'failed')::bigint  AS failed,
    COALESCE(EXTRACT(EPOCH FROM (now() - min(created_at) FILTER (WHERE state = 'pending')))::bigint, 0)::bigint AS oldest_pending_age_seconds
FROM storage_migration_objects;

-- name: StorageMigrationRecentFailures :many
-- Dead-lettered objects for the merged failures list. It reports the CAMPAIGN id,
-- never object_key: the overview contract forbids storage keys, and the campaign
-- is the actionable handle anyway (the key is in the campaign's own detail view).
SELECT campaign_id AS id, last_error AS error, attempts, updated_at
FROM storage_migration_objects
WHERE state = 'failed'
ORDER BY updated_at DESC, object_key
LIMIT $1;
