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
-- Atomically claims the new-follower backfill for one (remote HOST, channel):
-- 1 = claimed, 0 = refused because
--   * that host was backfilled for that channel inside the cooldown, or
--   * that host already had host_channel_cap channels backfilled in the last
--     day, or
--   * this instance has already backfilled daily_cap distinct hosts in the last
--     day and this host is not one of them (one server can mint any number of
--     host names).
-- Concurrent Follows race on the primary key, so at most one wins per pair; the
-- caps are ceilings, not exact counts, under concurrency.
-- TWIN of ClaimInstanceFollowerBackfill (core#362); dedupe once that lands.
INSERT INTO channel_follower_backfills (host, channel_id, last_backfill_at)
SELECT lower(sqlc.arg('host')), sqlc.arg('channel_id')::uuid, now()
WHERE (SELECT count(*) FROM channel_follower_backfills
       WHERE host = lower(sqlc.arg('host')) AND last_backfill_at > now() - interval '1 day')
      < sqlc.arg('host_channel_cap')::int
  AND (EXISTS (SELECT 1 FROM channel_follower_backfills
               WHERE host = lower(sqlc.arg('host')) AND last_backfill_at > now() - interval '1 day')
       OR (SELECT count(DISTINCT host) FROM channel_follower_backfills
           WHERE last_backfill_at > now() - interval '1 day') < sqlc.arg('daily_cap')::int)
ON CONFLICT (host, channel_id) DO UPDATE SET last_backfill_at = now()
WHERE channel_follower_backfills.last_backfill_at
      <= now() - make_interval(secs => sqlc.arg('cooldown_seconds')::int);
