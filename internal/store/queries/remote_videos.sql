-- Remote videos — metadata-only mirrors of federated videos (migration 0050,
-- .ralph/specs/federation-remote-content.md §1-2,5).

-- name: UpsertRemoteVideo :one
-- Idempotent ingestion keyed by the AP object id. A re-delivered or updated
-- object refreshes the bounded metadata; an existing cached thumbnail is kept
-- (thumbnail_key is only written via SetRemoteVideoThumbnail).
INSERT INTO remote_videos (
    object_url, remote_actor_url, title, description, duration_seconds,
    published_at, watch_url, stream_url
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (object_url) DO UPDATE SET
    remote_actor_url = EXCLUDED.remote_actor_url,
    title            = EXCLUDED.title,
    description      = EXCLUDED.description,
    duration_seconds = EXCLUDED.duration_seconds,
    published_at     = EXCLUDED.published_at,
    watch_url        = EXCLUDED.watch_url,
    stream_url       = EXCLUDED.stream_url,
    updated_at       = now()
RETURNING id, thumbnail_key;

-- name: SetRemoteVideoThumbnail :exec
-- Records the locally cached poster key (remote-thumbnails/<id>.jpg) after a
-- successful best-effort fetch.
UPDATE remote_videos SET thumbnail_key = $2, updated_at = now() WHERE id = $1;

-- name: GetRemoteVideoByID :one
-- The remote-watch read model. Content from admin-blocked instances is hidden
-- from all surfaces (§8), so a video whose origin domain is blocked is absent —
-- and so is an individually admin-blocked remote video (remote_video_blocks).
SELECT rv.id, rv.object_url, rv.remote_actor_url, ra.domain, rv.title,
       rv.description, rv.duration_seconds, rv.published_at, rv.watch_url,
       rv.stream_url, rv.thumbnail_key, rv.fetched_at, rv.updated_at
FROM remote_videos rv
JOIN remote_actors ra ON ra.actor_url = rv.remote_actor_url
WHERE rv.id = $1
  AND NOT EXISTS (SELECT 1 FROM blocked_instances b WHERE b.domain = ra.domain)
  AND NOT EXISTS (SELECT 1 FROM remote_video_blocks rb WHERE rb.remote_video_id = rv.id);

-- name: GetRemoteVideoByObjectURL :one
-- Resolve a remote video by its ActivityPub object id — the key inbound
-- Delete authority checks use (§7). Unfiltered by moderation state (an
-- origin's Delete applies to blocked rows too).
SELECT id, object_url, remote_actor_url
FROM remote_videos
WHERE object_url = $1;

-- name: DeleteRemoteVideoByObjectURL :execrows
-- Inbound Delete of a remote video (§7): the origin retracted it.
DELETE FROM remote_videos WHERE object_url = $1;

-- name: GetRemoteVideoByURL :one
-- Resolve a remote video by ANY url a person might paste: its ActivityPub
-- object id, or the human watch page the origin advertised (A29-F3).
--
-- A29 measured the gap this closes: B already held the exact object_url a
-- viewer pasted, and ResolveSearchTarget went straight to the network anyway —
-- so a video the instance was already storing could not be found by its own
-- URL. Consulting the store first is also the only way the /v/{code} form can
-- ever resolve: that path belongs to the origin's frontend and answers no
-- ActivityPub, so there is nothing to dereference, only something to remember.
SELECT id, object_url, remote_actor_url
FROM remote_videos
WHERE object_url = $1 OR (watch_url <> '' AND watch_url = $1);

-- name: UpsertRemoteVideoComment :one
-- Store (or re-store) one federated comment on a remote video (A29-F8,
-- migration 0140). Deduped by the ORIGIN's object id, so a redelivery cannot
-- double the thread and an Update{Note} edits in place.
--
-- `edited` is set by the CONFLICT arm only: the first arrival is not an edit,
-- and a redelivery of the same body must not claim to be one either, which is
-- why the flag ORs the existing value with a real body change.
INSERT INTO remote_video_comments (
    remote_video_id, remote_actor_url, remote_author_name, object_url, body, published_at
)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (object_url) DO UPDATE SET
    body        = EXCLUDED.body,
    edited      = remote_video_comments.edited OR remote_video_comments.body <> EXCLUDED.body,
    updated_at  = now()
RETURNING id, remote_video_id, remote_actor_url, object_url, body, edited;

-- name: ListRemoteVideoComments :many
-- One remote video's mirrored thread, oldest first — the order the origin's own
-- page shows. Rows from an admin-blocked instance and from an actor the VIEWER
-- blocks are excluded, the same two filters the remote-video feed applies, so a
-- thread can never show what the card would have hidden. viewer_id is NULL for
-- an anonymous caller, which makes the per-viewer clause trivially true.
SELECT c.id, c.remote_actor_url, c.remote_author_name, c.object_url, c.body,
       c.edited, c.published_at, c.created_at,
       COALESCE(ra.domain, '')::text AS domain
FROM remote_video_comments c
JOIN remote_actors ra ON ra.actor_url = c.remote_actor_url
WHERE c.remote_video_id = sqlc.arg('remote_video_id')
  AND NOT EXISTS (SELECT 1 FROM blocked_instances bi WHERE bi.domain = ra.domain)
  AND NOT EXISTS (
      SELECT 1 FROM muted_instances mi
      WHERE mi.muter_id = sqlc.narg('viewer_id') AND mi.domain = ra.domain
  )
  AND NOT EXISTS (
      SELECT 1 FROM remote_actor_block_reach rab
      WHERE (rab.blocker_id = sqlc.narg('viewer_id') OR rab.blocker_id IS NULL)
        AND rab.actor_url = c.remote_actor_url
  )
ORDER BY c.created_at, c.id
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountRemoteVideoComments :one
-- How many rows ListRemoteVideoComments would return for the same viewer.
SELECT count(*)::bigint
FROM remote_video_comments c
JOIN remote_actors ra ON ra.actor_url = c.remote_actor_url
WHERE c.remote_video_id = sqlc.arg('remote_video_id')
  AND NOT EXISTS (SELECT 1 FROM blocked_instances bi WHERE bi.domain = ra.domain)
  AND NOT EXISTS (
      SELECT 1 FROM muted_instances mi
      WHERE mi.muter_id = sqlc.narg('viewer_id') AND mi.domain = ra.domain
  )
  AND NOT EXISTS (
      SELECT 1 FROM remote_actor_block_reach rab
      WHERE (rab.blocker_id = sqlc.narg('viewer_id') OR rab.blocker_id IS NULL)
        AND rab.actor_url = c.remote_actor_url
  );

-- name: GetRemoteVideoCommentByObjectURL :one
-- Resolve a mirrored comment by the origin's object id — the authority check an
-- inbound Update{Note} or Delete runs before it may touch the row.
SELECT c.id, c.remote_video_id, c.remote_actor_url, c.object_url
FROM remote_video_comments c
WHERE c.object_url = $1;

-- name: DeleteRemoteVideoCommentByObjectURL :execrows
DELETE FROM remote_video_comments WHERE object_url = $1;
