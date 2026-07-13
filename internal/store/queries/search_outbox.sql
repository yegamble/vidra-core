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
-- Due, still-pending events oldest first. A single worker drains sequentially
-- (no row locking needed yet), building the batched /internal/v1/events envelope.
SELECT id, event_id, event_type, payload, attempts, created_at
FROM search_outbox
WHERE state = 'pending' AND next_attempt_at <= now()
ORDER BY id
LIMIT $1;

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
       v.category, v.language, v.is_sensitive,
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
       v.category, v.language, v.is_sensitive,
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
