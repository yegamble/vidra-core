-- Durable outbound queue + search-document sources for the vidra-search
-- integration (migration 0092, search-service W4). Mirrors the federation
-- delivery queue's claim/reschedule/dead-letter shape.

-- name: EnqueueSearchEvent :exec
-- Best-effort append of one event to the search outbox (single INSERT). The
-- caller logs + metrics on failure and NEVER fails the request path — a search
-- event is non-authoritative and the reconcile sweep is the backstop.
INSERT INTO search_outbox (event_type, payload)
VALUES ($1, $2);

-- name: ClaimDueSearchEvents :many
-- LEASES due, still-pending events oldest first, building the batched
-- /internal/v1/events envelope.
--
-- Previously a bare SELECT: two nodes would build two envelopes from the same
-- rows and both POST them. The search service dedupes on event_id so the index
-- stays correct, which is why this is the least harmful of the three -- but it is
-- still double the requests and double the payload for no benefit.
-- The lease is next_attempt_at pushed forward: a claimed row stops being due for
-- lease_seconds, so a second worker's claim skips it and a CRASHED worker's row
-- becomes due again by itself. FOR UPDATE SKIP LOCKED makes concurrent claimers
-- take disjoint rows without blocking each other.
-- search_outbox has no updated_at column, so only the lease is written.
-- The claim is wrapped in a CTE with an OUTER ORDER BY because UPDATE ...
-- RETURNING does not preserve the order of the subquery that chose the rows --
-- PostgreSQL returns them in whatever order it updated them. The "oldest first"
-- contract is real: these rows carry ordered side effects (an index mutation
-- applied out of order leaves the index stale; activities delivered out of order
-- are visible to the remote server), so the ordering has to be restated here.
WITH claimed AS (
    UPDATE search_outbox
    SET next_attempt_at = now() + (sqlc.arg(lease_seconds)::int * interval '1 second')
    WHERE id IN (
        SELECT id FROM search_outbox
        WHERE state = 'pending' AND next_attempt_at <= now()
        ORDER BY id
        LIMIT sqlc.arg(batch_size)
        FOR UPDATE SKIP LOCKED
    )
    RETURNING id, event_id, event_type, payload, attempts, created_at
)
SELECT id, event_id, event_type, payload, attempts, created_at
FROM claimed
ORDER BY id;

-- name: MarkSearchEventDelivered :exec
UPDATE search_outbox SET state = 'delivered' WHERE id = $1;

-- name: RescheduleSearchEvent :exec
UPDATE search_outbox
SET attempts = attempts + 1, next_attempt_at = $2, last_error = $3
WHERE id = $1;

-- name: DeadLetterSearchEvent :exec
UPDATE search_outbox
SET state = 'dead', attempts = attempts + 1, last_error = $2
WHERE id = $1;

-- name: SearchOutboxDepth :many
-- Queue-depth gauge source (vidra_queue_depth{queue="search_outbox",state=...}).
SELECT state, count(*)::bigint AS depth
FROM search_outbox
GROUP BY state;

-- name: GetVideoSearchDoc :one
-- Full search-document payload for one local video: the video, its owning
-- channel + account, aggregate view/like counts, duration, and tag set. Drives
-- the video.upsert event payload. Eligibility is DERIVED in the search service
-- from privacy+state+owner_unlisted, so those raw fields ride along.
SELECT v.id, v.channel_id, v.title, v.description, v.privacy, v.state,
       v.category, v.language, v.license, v.is_sensitive,
       v.created_at, v.updated_at, v.publish_at,
       c.handle AS channel_handle, c.display_name AS channel_name,
       c.owner_id,
       au.display_name AS owner_display_name,
       au.unlisted AS owner_unlisted,
       vm.duration_seconds,
       COALESCE(vc.views, 0)::bigint AS views,
       (SELECT count(*) FROM video_ratings r WHERE r.video_id = v.id AND r.rating = 'like')::bigint AS likes,
       COALESCE(
           (SELECT array_agg(t.tag ORDER BY t.tag) FROM video_tags t WHERE t.video_id = v.id),
           '{}'
       )::text[] AS tags
FROM videos v
JOIN channels c ON c.id = v.channel_id
JOIN users au ON au.id = c.owner_id
LEFT JOIN video_metadata vm ON vm.video_id = v.id
LEFT JOIN video_view_counts vc ON vc.video_id = v.id
WHERE v.id = $1;

-- name: ListVideoSearchDocsPage :many
-- One keyset page of eligible (public, published, not blocked, owner not
-- unlisted) local video search docs for the reconcile sweep, ordered by id for
-- stable paging (id > after; pass the nil UUID for the first page). Same column
-- projection as GetVideoSearchDoc so both build the identical doc payload.
SELECT v.id, v.channel_id, v.title, v.description, v.privacy, v.state,
       v.category, v.language, v.license, v.is_sensitive,
       v.created_at, v.updated_at, v.publish_at,
       c.handle AS channel_handle, c.display_name AS channel_name,
       c.owner_id,
       au.display_name AS owner_display_name,
       au.unlisted AS owner_unlisted,
       vm.duration_seconds,
       COALESCE(vc.views, 0)::bigint AS views,
       (SELECT count(*) FROM video_ratings r WHERE r.video_id = v.id AND r.rating = 'like')::bigint AS likes,
       COALESCE(
           (SELECT array_agg(t.tag ORDER BY t.tag) FROM video_tags t WHERE t.video_id = v.id),
           '{}'
       )::text[] AS tags
FROM videos v
JOIN channels c ON c.id = v.channel_id
JOIN users au ON au.id = c.owner_id
LEFT JOIN video_metadata vm ON vm.video_id = v.id
LEFT JOIN video_view_counts vc ON vc.video_id = v.id
WHERE v.privacy = 'public' AND v.state = 'published'
  AND NOT au.unlisted
  AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
  AND v.id > sqlc.arg('after')
ORDER BY v.id
LIMIT sqlc.arg('page_size');

-- name: PruneSearchOutbox :execrows
-- One batch of expired TERMINAL outbox rows, oldest first (migration 0122).
--
-- Called once per prunable state so 'delivered' and 'dead' can carry different
-- windows: a delivered row is finished work the search service already holds,
-- while a dead row is the only surviving evidence of WHY a delivery failed.
--
-- `state <> 'pending'` is a structural guard, not a redundancy. It is what makes
-- an undelivered row undeletable by this query no matter what a caller passes:
-- a pending row is an index mutation, or a privacy-critical user.history_deleted
-- purge, that has NOT happened yet, and deleting one loses it silently -- there
-- is no second copy and nothing would ever notice. The Go worker never asks for
-- 'pending'; this is what happens if some future caller does.
--
-- Batched like PruneQoEEvents, and ordered by (created_at, id) for the same
-- reason the due claim restates its order: an unbounded DELETE would hold locks
-- across the whole backlog while the drainer is trying to claim from the same
-- table. Unlike qoe_events the id here is a BIGSERIAL, so it agrees with
-- created_at rather than being random -- it is the tiebreaker, and the ordering
-- rides search_outbox_prune_idx either way.
DELETE FROM search_outbox
WHERE id IN (
    SELECT o.id FROM search_outbox o
    WHERE o.state = sqlc.arg('state')
      AND o.state <> 'pending'
      AND o.created_at < sqlc.arg('cutoff')
    ORDER BY o.created_at, o.id
    LIMIT sqlc.arg('batch_size')
);

-- name: PurgeUserSearchOutbox :execrows
-- Erase one user's own search data from core's PRIMARY database (migration 0123).
--
-- This is the local half of "Clear search history" and of an account deletion.
-- Both used to proxy to vidra-search and touch NOTHING here, while a server-side
-- search.submitted payload carries the raw query text next to the user_id -- so
-- the searches the modal called permanently removed survived in core.
--
-- Two rules make this correct, and both are structural (in the SQL, not in the
-- caller) for the same reason `state <> 'pending'` is structural in
-- PruneSearchOutbox: a caller cannot vary them, at any call site, ever.
--
--   1. `payload->>'user_id' = $1` is the WHOLE subject predicate. It matches only
--      rows whose payload names this user at the TOP LEVEL: the behavioural
--      events (search.submitted, the POST /search/events pass-through) and
--      video.watch_progress. It deliberately does NOT match video.upsert or
--      reconcile.page, which name an owner_id NESTED under "video" -- those are
--      the public catalogue, not the user's behaviour, and erasing a creator's
--      documents on "clear my search history" would silently deindex their
--      videos. It is an equality on the extracted text, never a substring match
--      on the serialized payload, for exactly that reason.
--
--   2. The user.suppress / user.history_deleted exclusion is the subtle one, and
--      it is not an optimisation. Those two rows are not data ABOUT the user;
--      they are the durable INSTRUCTION that performs the erasure downstream in
--      vidra-search, and they carry a top-level user_id exactly like the rows
--      being deleted. Purging them alongside would cancel the erasure it was
--      meant to complete -- and it would do so silently, because a pending
--      outbox row has no second copy and nothing downstream ever notices one
--      that vanished. This has to hold for an event queued moments ago by THIS
--      request AND for one an earlier clear left pending, so ordering the two
--      statements is not sufficient on its own; the exclusion is what makes it
--      safe.
--
-- Not batched, unlike PruneSearchOutbox. That query walks the whole table and
-- must bound its lock footprint; this one is bounded to a single user's own rows
-- via search_outbox_user_purge_idx and runs a handful of times a day. One
-- statement also makes an erasure atomic: a partially applied erasure that
-- reported success is worse than one that failed and can be retried.
DELETE FROM search_outbox
WHERE payload->>'user_id' = sqlc.arg('user_id')::text
  AND event_type <> 'user.suppress'
  AND event_type <> 'user.history_deleted';
