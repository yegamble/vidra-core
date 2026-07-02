-- name: CreateLiveStream :one
-- Create a live stream for a channel with a pre-hashed stream key. The raw key is
-- returned to the caller once by the service; only the hash is stored here.
INSERT INTO live_streams (channel_id, title, description, privacy, permanent, stream_key_hash)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, channel_id, title, description, privacy, state, permanent, created_at, updated_at;

-- name: GetLiveStreamByID :one
-- One live stream joined with its owning channel (owner_id for authz, handle +
-- display name for display). Never returns the stream key hash.
SELECT ls.id, ls.channel_id, ls.title, ls.description, ls.privacy, ls.state, ls.permanent,
       ls.created_at, ls.updated_at,
       ch.owner_id, ch.handle AS channel_handle, ch.display_name AS channel_display_name
FROM live_streams ls
JOIN channels ch ON ch.id = ls.channel_id
WHERE ls.id = $1;

-- name: ListLiveStreamsByChannel :many
-- A channel's live streams, newest first (owner management list). No key hash.
SELECT id, channel_id, title, description, privacy, state, permanent, created_at, updated_at
FROM live_streams
WHERE channel_id = $1
ORDER BY created_at DESC, id;

-- name: UpdateLiveStreamKey :exec
-- Rotate a stream's key (store the new hash).
UPDATE live_streams SET stream_key_hash = $2, updated_at = now() WHERE id = $1;

-- name: GetLiveStreamByKeyHash :one
-- Look up a stream by its key hash — the RTMP ingest boundary authenticates a
-- publisher by hashing the presented stream key. Returns id, channel, permanent
-- (so stop can decide ended vs offline), and current state.
SELECT id, channel_id, permanent, state
FROM live_streams
WHERE stream_key_hash = $1;

-- name: SetLiveStreamState :exec
-- Flip a stream's live state (offline/live/ended), set by the ingest boundary.
UPDATE live_streams SET state = $2, updated_at = now() WHERE id = $1;

-- name: DeleteLiveStream :execrows
DELETE FROM live_streams WHERE id = $1;
