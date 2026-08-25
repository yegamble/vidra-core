-- name: CreateLiveStream :one
-- Create a live stream for a channel with a pre-hashed stream key. The raw key is
-- returned to the caller once by the service; only the hash is stored here.
INSERT INTO live_streams (channel_id, title, description, privacy, permanent, replay_enabled, stream_key_hash)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, channel_id, title, description, privacy, state, permanent, replay_enabled, started_at, created_at, updated_at;

-- name: GetLiveStreamByID :one
-- One live stream joined with its owning channel (owner_id for authz, handle +
-- display name for display). Never returns the stream key hash.
SELECT ls.id, ls.channel_id, ls.title, ls.description, ls.privacy, ls.state, ls.permanent,
       ls.replay_enabled, ls.started_at, ls.created_at, ls.updated_at,
       ch.owner_id, ch.handle AS channel_handle, ch.display_name AS channel_display_name
FROM live_streams ls
JOIN channels ch ON ch.id = ls.channel_id
WHERE ls.id = $1;

-- name: ListLiveStreamsByChannel :many
-- A channel's live streams, newest first (owner management list). No key hash.
SELECT id, channel_id, title, description, privacy, state, permanent, replay_enabled, started_at, created_at, updated_at
FROM live_streams
WHERE channel_id = sqlc.arg('channel_id')
ORDER BY created_at DESC, id
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountLiveStreamsByChannel :one
-- How many rows ListLiveStreamsByChannel would return, ignoring pagination.
SELECT count(*)::bigint FROM live_streams WHERE channel_id = sqlc.arg('channel_id');

-- name: ListLivePublicStreams :many
-- Public "Live now" listing: currently-live PUBLIC streams across all channels,
-- most-recently-started first. Unlisted/private streams and offline/ended streams
-- never appear. Joined with the owning channel for display; never the key hash.
SELECT ls.id, ls.title, ls.description, ls.started_at,
       ch.handle AS channel_handle, ch.display_name AS channel_display_name
FROM live_streams ls
JOIN channels ch ON ch.id = ls.channel_id
WHERE ls.state = 'live' AND ls.privacy = 'public'
ORDER BY ls.started_at DESC NULLS LAST, ls.id
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountLivePublicStreams :one
-- How many rows ListLivePublicStreams would return, ignoring pagination. The
-- channels JOIN is part of the predicate. CountLiveStreamsLive below counts
-- ALL live sessions (including private/unlisted) for the instance-wide publish
-- cap and would over-report this public rail.
SELECT count(*)::bigint
FROM live_streams ls
JOIN channels ch ON ch.id = ls.channel_id
WHERE ls.state = 'live' AND ls.privacy = 'public';

-- name: UpdateLiveStreamKey :exec
-- Rotate a stream's key (store the new hash).
UPDATE live_streams SET stream_key_hash = $2, updated_at = now() WHERE id = $1;

-- name: UpdateLiveStream :one
-- Edit a stream's mutable metadata (title/description/privacy/permanent/
-- replay_enabled). The caller has already authorised ownership. Never touches the
-- stream key or live state.
UPDATE live_streams
SET title = $2, description = $3, privacy = $4, permanent = $5, replay_enabled = $6, updated_at = now()
WHERE id = $1
RETURNING id, channel_id, title, description, privacy, state, permanent, replay_enabled, started_at, created_at, updated_at;

-- name: GetLiveStreamByKeyHash :one
-- Look up a stream by its key hash — the RTMP ingest boundary authenticates a
-- publisher by hashing the presented stream key. Returns id, channel + owner
-- (the per-user simultaneous-lives cap counts by owner, config-parity W11),
-- permanent (so stop can decide ended vs offline), and current state.
SELECT ls.id, ls.channel_id, ls.permanent, ls.state, ch.owner_id
FROM live_streams ls
JOIN channels ch ON ch.id = ls.channel_id
WHERE ls.stream_key_hash = $1;

-- name: CountLiveStreamsLive :one
-- How many sessions are currently live across the instance (the
-- live_max_instance_lives publish-callback cap, config-parity W11).
SELECT count(*) FROM live_streams WHERE state = 'live';

-- name: CountLiveStreamsLiveByOwner :one
-- How many sessions the given user currently has live across all their
-- channels (the live_max_user_lives publish-callback cap, config-parity W11).
SELECT count(*)
FROM live_streams ls
JOIN channels ch ON ch.id = ls.channel_id
WHERE ls.state = 'live' AND ch.owner_id = $1;

-- name: ListOverdueLiveStreams :many
-- Currently-live sessions that started before the cutoff — the
-- live_max_duration_secs watchdog force-closes these (config-parity W11).
-- started_at IS NOT NULL guards streams that went live before started_at
-- tracking existed (migration 0076): with no session start there is nothing
-- truthful to measure, so they are never force-closed.
SELECT id, permanent, started_at
FROM live_streams
WHERE state = 'live' AND started_at IS NOT NULL AND started_at < $1
ORDER BY started_at, id;

-- name: SetLiveStreamState :exec
-- Flip a stream's live state (offline/live/ended), set by the ingest boundary.
-- Entering 'live' from a non-live state stamps started_at (the current session's
-- start, surfaced by the public "Live now" listing); an idempotent re-assert of
-- 'live' (e.g. the post-rename re-invocation) preserves the original start;
-- leaving live (offline/ended) clears it.
UPDATE live_streams
SET state = $2,
    started_at = CASE
        WHEN $2 = 'live' AND state <> 'live' THEN now()
        WHEN $2 = 'live' THEN started_at
        ELSE NULL
    END,
    updated_at = now()
WHERE id = $1;

-- name: DeleteLiveStream :execrows
DELETE FROM live_streams WHERE id = $1;
