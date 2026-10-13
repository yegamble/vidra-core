-- name: ListChannelBackfillVideoIDs :many
-- The videos a NEW remote follower of a channel is pushed, newest first. This is
-- the public-discovery predicate every listing carries (ListPublicVideosSorted,
-- ListLivePublicStreams): public, published, not moderator-blocked, owner active
-- and not account-unlisted (§16) — plus the owner not being deleted. NOT the
-- channel outbox's thinner one (ListChannelOutboxVideos checks neither owner
-- clause): a push to a stranger's inbox must never carry what discovery hides.
-- The channel's own activitypub_enabled is checked by the caller.
SELECT v.id
FROM videos v
JOIN channels c ON c.id = v.channel_id
JOIN users u ON u.id = c.owner_id
WHERE v.channel_id = sqlc.arg('channel_id')
  AND v.privacy = 'public' AND v.state = 'published'
  AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
  AND u.is_active = TRUE AND NOT u.unlisted AND u.deleted_at IS NULL
ORDER BY v.created_at DESC, v.id DESC
LIMIT sqlc.arg('result_limit');

-- name: ClaimChannelFollowerBackfill :execrows
-- Atomically claims the new-follower backfill for one remote HOST:
-- 1 = claimed, 0 = refused — the host was backfilled inside the cooldown, or this
-- instance has already backfilled daily_cap hosts in the last day. Concurrent
-- Follows of one host race on the primary key, so at most one wins; the
-- instance-wide cap is a ceiling, not an exact count, under concurrency.
-- TWIN of ClaimInstanceFollowerBackfill (core#362); dedupe once that lands.
INSERT INTO channel_follower_backfills (host, last_backfill_at)
SELECT lower(sqlc.arg('host')), now()
WHERE (SELECT count(*) FROM channel_follower_backfills
       WHERE last_backfill_at > now() - interval '1 day') < sqlc.arg('daily_cap')::int
ON CONFLICT (host) DO UPDATE SET last_backfill_at = now()
WHERE channel_follower_backfills.last_backfill_at
      <= now() - make_interval(secs => sqlc.arg('cooldown_seconds')::int);
