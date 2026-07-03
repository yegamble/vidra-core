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
-- from all surfaces (§8), so a video whose origin domain is blocked is absent.
SELECT rv.id, rv.object_url, rv.remote_actor_url, ra.domain, rv.title,
       rv.description, rv.duration_seconds, rv.published_at, rv.watch_url,
       rv.stream_url, rv.thumbnail_key, rv.fetched_at, rv.updated_at
FROM remote_videos rv
JOIN remote_actors ra ON ra.actor_url = rv.remote_actor_url
WHERE rv.id = $1
  AND NOT EXISTS (SELECT 1 FROM blocked_instances b WHERE b.domain = ra.domain);
