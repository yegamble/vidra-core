-- name: CreateVideo :one
INSERT INTO videos (channel_id, title, description, privacy, category, language, license, publish_at, is_sensitive, comments_policy, download_enabled, publish_after_transcode, sensitive_reason)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING id, channel_id, title, description, privacy, state, created_at, updated_at, category, language, license, publish_at, embed_privacy, embed_allowed_domains, is_sensitive, comments_policy, download_enabled, publish_after_transcode, pinned_comment_id, sensitive_reason, originally_published_at, short_code, peertube_uuid;

-- name: CountPublicVideos :one
-- Public, published videos — the "local posts" count NodeInfo advertises. Only
-- these ever federate, so this is the right public-facing total.
SELECT count(*) FROM videos WHERE privacy = 'public' AND state = 'published';

-- name: CountPublicVideosByChannel :one
-- Public, published videos for one channel — the totalItems of its AP outbox.
SELECT count(*) FROM videos WHERE channel_id = $1 AND privacy = 'public' AND state = 'published';

-- name: ListChannelOutboxVideos :many
-- One page of a channel's public, published videos (newest first) for the AP
-- outbox collection — just the fields needed to render a Create{Video}.
-- short_code builds the object's `url` (the human watch page). Its `id` stays
-- the uuid form and must never move: remote servers key on it.
SELECT id, title, description, short_code
FROM videos
WHERE channel_id = $1 AND privacy = 'public' AND state = 'published'
ORDER BY created_at DESC, id DESC
LIMIT $2 OFFSET $3;

-- name: GetVideoByID :one
SELECT v.id, v.short_code, v.channel_id, v.title, v.description, v.privacy, v.state, v.created_at, v.updated_at,
       v.category, v.language, v.license, v.publish_at, v.originally_published_at,
       v.is_sensitive, v.sensitive_reason,
       v.comments_policy, v.download_enabled, v.publish_after_transcode,
       c.owner_id, c.handle AS channel_handle, c.display_name AS channel_display_name,
       au.display_name AS author_display_name
FROM videos v
JOIN channels c ON c.id = v.channel_id
JOIN users au ON au.id = c.owner_id
WHERE v.id = $1;

-- name: ListVideoIDsByOwner :many
-- Every video (any privacy/state) owned by a user, resolved via their channels.
-- Drives the IPFS mirror's unlisted-toggle re-evaluation (spec §3): flipping a user
-- unlisted must pull ALL their public videos (and derivatives) off the mirror, and
-- re-listing re-pins the still-eligible ones. Returns ALL of the owner's videos —
-- SyncVideo re-derives per-video eligibility (pin vs unpin) from committed state.
SELECT v.id
FROM videos v
JOIN channels c ON c.id = v.channel_id
WHERE c.owner_id = $1
ORDER BY v.id;

-- name: ListVideosByChannel :many
-- A channel's videos (owner view, all states) with discovery-card data plus
-- publish_at so the studio can badge scheduled videos. Paginated: this used to
-- have no LIMIT at all, so a channel with 50k videos serialised 50k rows on
-- every studio page load. sort accepts 'published_at' (oldest first) or
-- '-published_at' (newest first, the previous fixed behaviour and the default);
-- the id tiebreaker makes page boundaries stable.
SELECT v.id, v.channel_id, v.title, v.description, v.privacy, v.state,
       v.created_at, v.updated_at, v.publish_at,
       COALESCE(vc.views, 0)::bigint AS views,
       EXISTS (
           SELECT 1 FROM video_files f
           WHERE f.video_id = v.id AND f.kind = 'thumbnail'
       ) AS has_thumbnail,
       c.handle AS channel_handle, c.display_name AS channel_display_name,
       au.display_name AS author_display_name,
       vm.duration_seconds, v.is_sensitive, v.sensitive_reason, v.short_code,
       -- A moderator block changes neither state nor privacy, so without this
       -- column the owner's own management list shows a blocked video as
       -- 'published' while it 404s for everyone including them (A16 slice 2).
       -- It is selected only on the OWNER view; the public listing below never
       -- returns a blocked row at all.
       EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id) AS blocked,
       -- ...and WHY, which the A16 ruling made creator-facing: a creator told
       -- only that something was taken down cannot appeal it or fix the next
       -- upload. The subselect is correlated on the same row as `blocked`, so an
       -- unblocked video reads '' and the marker and its reason can never
       -- disagree. This is the OWNER listing; no public query selects it.
       COALESCE(
           (SELECT b.reason FROM video_blocks b WHERE b.video_id = v.id),
           ''
       )::text AS block_reason
FROM videos v
JOIN channels c ON c.id = v.channel_id
JOIN users au ON au.id = c.owner_id
LEFT JOIN video_view_counts vc ON vc.video_id = v.id
LEFT JOIN video_metadata vm ON vm.video_id = v.id
WHERE v.channel_id = sqlc.arg('channel_id')
ORDER BY
    CASE WHEN sqlc.arg('sort')::text = 'published_at' THEN v.created_at END ASC,
    CASE WHEN sqlc.arg('sort')::text <> 'published_at' THEN v.created_at END DESC,
    v.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountVideosByChannel :one
-- How many rows ListVideosByChannel would return, ignoring pagination.
SELECT count(*)::bigint FROM videos v WHERE v.channel_id = sqlc.arg('channel_id');

-- name: ListVideoIDsByChannel :many
-- Every video id in a channel, UNPAGINATED — the account-deletion sweep, which
-- must visit all of them (a delete that stopped at the UI page size would leave
-- orphaned blobs). Kept separate from ListVideosByChannel so the paginated UI
-- list can never lose its LIMIT again; only the id is needed here.
SELECT id FROM videos WHERE channel_id = $1 ORDER BY id;

-- name: ListPublicVideosByChannel :many
-- A channel's public, published videos with discovery-card data.
SELECT v.id, v.channel_id, v.title, v.description, v.privacy, v.state,
       v.created_at, v.updated_at,
       COALESCE(vc.views, 0)::bigint AS views,
       EXISTS (
           SELECT 1 FROM video_files f
           WHERE f.video_id = v.id AND f.kind = 'thumbnail'
       ) AS has_thumbnail,
       c.handle AS channel_handle, c.display_name AS channel_display_name,
       au.display_name AS author_display_name,
       vm.duration_seconds, v.is_sensitive, v.sensitive_reason, v.short_code
FROM videos v
JOIN channels c ON c.id = v.channel_id
JOIN users au ON au.id = c.owner_id
LEFT JOIN video_view_counts vc ON vc.video_id = v.id
LEFT JOIN video_metadata vm ON vm.video_id = v.id
WHERE v.channel_id = sqlc.arg('channel_id') AND v.privacy = 'public' AND v.state = 'published'
  AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
  -- A16 ruling: the per-viewer mute/block clause, verbatim from
  -- ListPublicVideosSorted. It was missing here alone, so a muted account's own
  -- channel page kept listing everything the mute hid everywhere else, and an
  -- autosuggest channel hit linked straight to it. viewer_id is NULL for an
  -- anonymous caller, which makes both NOT EXISTS trivially true.
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts m
      WHERE m.muter_id = sqlc.narg('viewer_id') AND m.muted_id = c.owner_id
  )
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks ub
      WHERE ub.blocker_id = sqlc.narg('viewer_id') AND ub.blocked_id = c.owner_id
  )
  -- Sensitive-content "hide" moved INTO the query (it used to be a Go-side skip
  -- over the whole result set). With a LIMIT that filter has to be in SQL or a
  -- page would silently return fewer rows than asked for and the total would
  -- count rows the caller can never see.
  AND (NOT sqlc.arg('hide_sensitive')::bool OR NOT v.is_sensitive)
ORDER BY
    CASE WHEN sqlc.arg('sort')::text = 'published_at' THEN v.created_at END ASC,
    CASE WHEN sqlc.arg('sort')::text <> 'published_at' THEN v.created_at END DESC,
    v.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountPublicVideosByChannelVisible :one
-- How many rows ListPublicVideosByChannel would return, ignoring pagination.
-- The block predicate must stay identical: CountPublicVideosByChannel above
-- answers the AP outbox's question (it counts blocked videos too) and would
-- over-report this list. Same for the per-viewer mute/block clause added with
-- the A16 ruling — a total taken without it would promise the muter a page the
-- list cannot serve. The channels JOIN exists only to reach c.owner_id.
SELECT count(*)::bigint
FROM videos v
JOIN channels c ON c.id = v.channel_id
WHERE v.channel_id = sqlc.arg('channel_id') AND v.privacy = 'public' AND v.state = 'published'
  AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts m
      WHERE m.muter_id = sqlc.narg('viewer_id') AND m.muted_id = c.owner_id
  )
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks ub
      WHERE ub.blocker_id = sqlc.narg('viewer_id') AND ub.blocked_id = c.owner_id
  )
  AND (NOT sqlc.arg('hide_sensitive')::bool OR NOT v.is_sensitive);

-- name: ListPublicVideosSorted :many
-- The public feed, joined with view counts and thumbnail availability so cards
-- have what they need, ordered by the requested mode:
--   recent   -> newest first (the NULL CASE terms fall through to created_at)
--   popular  -> most all-time views first
--   trending -> views decayed by age (Hacker-News-style gravity)
-- Optional filters (NULL = off): tag (exact, tags are stored lowercased),
-- category, and language (taxonomy ids; validated by the HTTP layer).
-- include_remote (feed ?scope=all, remote-content §4) UNIONs in federated
-- remote videos (metadata-only cards flagged remote, carrying origin domain +
-- watch/stream URLs). Remote videos have no local taxonomy/tags, so any active
-- tag/category/language filter excludes them; they have no local views, so
-- popular/trending sort them after viewed local videos. Blocked instances hide
-- remote rows for everyone; a signed-in viewer's muted instances hide them too.
SELECT feed.id, feed.remote, feed.channel_id, feed.title, feed.description,
       feed.privacy, feed.state, feed.created_at, feed.updated_at, feed.views,
       feed.has_thumbnail, feed.channel_handle, feed.channel_display_name,
       feed.author_display_name,
       feed.duration_seconds, feed.domain, feed.watch_url, feed.stream_url,
       feed.is_sensitive, feed.sensitive_reason, feed.short_code
FROM (
    SELECT v.id,
           false AS remote,
           v.channel_id,
           v.title, v.description, v.privacy, v.state,
           v.created_at, v.updated_at,
           COALESCE(vc.views, 0)::bigint AS views,
           EXISTS (
               SELECT 1 FROM video_files f
               WHERE f.video_id = v.id AND f.kind = 'thumbnail'
           ) AS has_thumbnail,
           c.handle AS channel_handle, c.display_name AS channel_display_name,
           au.display_name AS author_display_name,
           vm.duration_seconds,
           ''::text AS domain,
           ''::text AS watch_url,
           NULL::text AS stream_url,
           v.is_sensitive,
           v.sensitive_reason,
           v.short_code
    FROM videos v
    JOIN channels c ON c.id = v.channel_id
    JOIN users au ON au.id = c.owner_id
    LEFT JOIN video_view_counts vc ON vc.video_id = v.id
    LEFT JOIN video_metadata vm ON vm.video_id = v.id
    WHERE v.privacy = 'public' AND v.state = 'published'
      AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
      AND NOT EXISTS (
          SELECT 1 FROM muted_accounts m
          WHERE m.muter_id = sqlc.narg('viewer_id') AND m.muted_id = c.owner_id
      )
      -- §13: owners the viewer has BLOCKED are hidden too (per-viewer, like mutes).
      AND NOT EXISTS (
          SELECT 1 FROM user_blocks ub
          WHERE ub.blocker_id = sqlc.narg('viewer_id') AND ub.blocked_id = c.owner_id
      )
      AND (sqlc.narg('tag')::text IS NULL OR EXISTS (
          SELECT 1 FROM video_tags t WHERE t.video_id = v.id AND t.tag = sqlc.narg('tag')
      ))
      AND (sqlc.narg('category')::text IS NULL OR v.category = sqlc.narg('category'))
      AND (sqlc.narg('language')::text IS NULL OR v.language = sqlc.narg('language'))
      -- Unlisted owners (§16) are excluded from discovery; direct URLs still serve.
      AND NOT EXISTS (SELECT 1 FROM users u WHERE u.id = c.owner_id AND u.unlisted)
      -- Sensitive-content policy "hide" (instance-platform-info): flagged videos
      -- drop out of PUBLIC discovery only (owner/admin/direct reads unfiltered).
      AND (NOT sqlc.arg('hide_sensitive')::bool OR NOT v.is_sensitive)
    UNION ALL
    SELECT rv.id,
           true AS remote,
           '00000000-0000-0000-0000-000000000000'::uuid AS channel_id,
           rv.title, rv.description, ''::text AS privacy, ''::text AS state,
           COALESCE(rv.published_at, rv.fetched_at) AS created_at, rv.updated_at,
           0::bigint AS views,
           (rv.thumbnail_key IS NOT NULL) AS has_thumbnail,
           (ra.preferred_username || '@' || ra.domain)::text AS channel_handle,
           ra.preferred_username AS channel_display_name,
           ''::text AS author_display_name,
           rv.duration_seconds,
           ra.domain,
           rv.watch_url,
           rv.stream_url,
           false AS is_sensitive,
           ''::text AS sensitive_reason,
           ''::text AS short_code
    FROM remote_videos rv
    JOIN remote_actors ra ON ra.actor_url = rv.remote_actor_url
    WHERE sqlc.arg('include_remote')::bool
      AND sqlc.narg('tag')::text IS NULL
      AND sqlc.narg('category')::text IS NULL
      AND sqlc.narg('language')::text IS NULL
      AND NOT EXISTS (SELECT 1 FROM blocked_instances bi WHERE bi.domain = ra.domain)
      AND NOT EXISTS (SELECT 1 FROM remote_video_blocks rb WHERE rb.remote_video_id = rv.id)
      AND NOT EXISTS (
          SELECT 1 FROM muted_instances mi
          WHERE mi.muter_id = sqlc.narg('viewer_id') AND mi.domain = ra.domain
      )
) AS feed
ORDER BY
    CASE WHEN sqlc.arg('sort')::text = 'popular' THEN feed.views END DESC,
    CASE WHEN sqlc.arg('sort')::text = 'trending'
         THEN feed.views::float8
              / power(EXTRACT(EPOCH FROM (now() - feed.created_at)) / 3600.0 + 2.0, 1.5)
    END DESC,
    feed.created_at DESC, feed.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountPublicVideosSorted :one
-- How many rows ListPublicVideosSorted would return for the same filters,
-- viewer and scope, ignoring pagination. The whole UNION body is repeated
-- verbatim because every one of its predicates is per-request: mutes and
-- blocks are per-VIEWER, and ?scope=local drops the remote arm entirely. A
-- total taken over anything wider would promise pages the feed cannot serve.
-- The sort mode is deliberately absent: ordering cannot change a count.
SELECT count(*)::bigint
FROM (
    SELECT v.id,
           false AS remote,
           v.channel_id,
           v.title, v.description, v.privacy, v.state,
           v.created_at, v.updated_at,
           COALESCE(vc.views, 0)::bigint AS views,
           EXISTS (
               SELECT 1 FROM video_files f
               WHERE f.video_id = v.id AND f.kind = 'thumbnail'
           ) AS has_thumbnail,
           c.handle AS channel_handle, c.display_name AS channel_display_name,
           au.display_name AS author_display_name,
           vm.duration_seconds,
           ''::text AS domain,
           ''::text AS watch_url,
           NULL::text AS stream_url,
           v.is_sensitive,
           v.sensitive_reason
    FROM videos v
    JOIN channels c ON c.id = v.channel_id
    JOIN users au ON au.id = c.owner_id
    LEFT JOIN video_view_counts vc ON vc.video_id = v.id
    LEFT JOIN video_metadata vm ON vm.video_id = v.id
    WHERE v.privacy = 'public' AND v.state = 'published'
      AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
      AND NOT EXISTS (
          SELECT 1 FROM muted_accounts m
          WHERE m.muter_id = sqlc.narg('viewer_id') AND m.muted_id = c.owner_id
      )
      -- §13: owners the viewer has BLOCKED are hidden too (per-viewer, like mutes).
      AND NOT EXISTS (
          SELECT 1 FROM user_blocks ub
          WHERE ub.blocker_id = sqlc.narg('viewer_id') AND ub.blocked_id = c.owner_id
      )
      AND (sqlc.narg('tag')::text IS NULL OR EXISTS (
          SELECT 1 FROM video_tags t WHERE t.video_id = v.id AND t.tag = sqlc.narg('tag')
      ))
      AND (sqlc.narg('category')::text IS NULL OR v.category = sqlc.narg('category'))
      AND (sqlc.narg('language')::text IS NULL OR v.language = sqlc.narg('language'))
      -- Unlisted owners (§16) are excluded from discovery; direct URLs still serve.
      AND NOT EXISTS (SELECT 1 FROM users u WHERE u.id = c.owner_id AND u.unlisted)
      -- Sensitive-content policy "hide" (instance-platform-info): flagged videos
      -- drop out of PUBLIC discovery only (owner/admin/direct reads unfiltered).
      AND (NOT sqlc.arg('hide_sensitive')::bool OR NOT v.is_sensitive)
    UNION ALL
    SELECT rv.id,
           true AS remote,
           '00000000-0000-0000-0000-000000000000'::uuid AS channel_id,
           rv.title, rv.description, ''::text AS privacy, ''::text AS state,
           COALESCE(rv.published_at, rv.fetched_at) AS created_at, rv.updated_at,
           0::bigint AS views,
           (rv.thumbnail_key IS NOT NULL) AS has_thumbnail,
           (ra.preferred_username || '@' || ra.domain)::text AS channel_handle,
           ra.preferred_username AS channel_display_name,
           ''::text AS author_display_name,
           rv.duration_seconds,
           ra.domain,
           rv.watch_url,
           rv.stream_url,
           false AS is_sensitive,
           ''::text AS sensitive_reason
    FROM remote_videos rv
    JOIN remote_actors ra ON ra.actor_url = rv.remote_actor_url
    WHERE sqlc.arg('include_remote')::bool
      AND sqlc.narg('tag')::text IS NULL
      AND sqlc.narg('category')::text IS NULL
      AND sqlc.narg('language')::text IS NULL
      AND NOT EXISTS (SELECT 1 FROM blocked_instances bi WHERE bi.domain = ra.domain)
      AND NOT EXISTS (SELECT 1 FROM remote_video_blocks rb WHERE rb.remote_video_id = rv.id)
      AND NOT EXISTS (
          SELECT 1 FROM muted_instances mi
          WHERE mi.muter_id = sqlc.narg('viewer_id') AND mi.domain = ra.domain
      )
) AS feed;

-- name: ListSubscriptionVideos :many
-- The "subscriptions" feed (remote-content §3): a UNION of public, published
-- videos from the LOCAL channels the user follows and the ingested remote
-- videos of their ACCEPTED remote-channel follows, newest first, with the same
-- discovery-card shape as the main feed. Remote cards are flagged remote and
-- carry the origin domain + watch/stream URLs; instance mutes (the user's) and
-- the admin instance blocklist hide remote rows.
SELECT feed.id, feed.remote, feed.channel_id, feed.title, feed.description,
       feed.privacy, feed.state, feed.created_at, feed.updated_at, feed.views,
       feed.has_thumbnail, feed.channel_handle, feed.channel_display_name,
       feed.author_display_name,
       feed.duration_seconds, feed.domain, feed.watch_url, feed.stream_url,
       feed.is_sensitive, feed.sensitive_reason, feed.short_code
FROM (
    SELECT v.id,
           false AS remote,
           v.channel_id,
           v.title, v.description, v.privacy, v.state,
           v.created_at, v.updated_at,
           COALESCE(vc.views, 0)::bigint AS views,
           EXISTS (
               SELECT 1 FROM video_files f
               WHERE f.video_id = v.id AND f.kind = 'thumbnail'
           ) AS has_thumbnail,
           c.handle AS channel_handle, c.display_name AS channel_display_name,
           au.display_name AS author_display_name,
           vm.duration_seconds,
           ''::text AS domain,
           ''::text AS watch_url,
           NULL::text AS stream_url,
           v.is_sensitive,
           v.sensitive_reason,
           v.short_code
    FROM videos v
    JOIN channels c ON c.id = v.channel_id
    JOIN users au ON au.id = c.owner_id
    LEFT JOIN video_view_counts vc ON vc.video_id = v.id
    LEFT JOIN video_metadata vm ON vm.video_id = v.id
    WHERE v.privacy = 'public' AND v.state = 'published'
      AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
      AND NOT EXISTS (
          SELECT 1 FROM muted_accounts m
          WHERE m.muter_id = sqlc.arg('follower_id') AND m.muted_id = c.owner_id
      )
      -- §13: owners the follower has BLOCKED are hidden too.
      AND NOT EXISTS (
          SELECT 1 FROM user_blocks ub
          WHERE ub.blocker_id = sqlc.arg('follower_id') AND ub.blocked_id = c.owner_id
      )
      AND v.channel_id IN (
          SELECT channel_id FROM channel_follows WHERE follower_id = sqlc.arg('follower_id')
      )
    UNION ALL
    SELECT rv.id,
           true AS remote,
           '00000000-0000-0000-0000-000000000000'::uuid AS channel_id,
           rv.title, rv.description, ''::text AS privacy, ''::text AS state,
           COALESCE(rv.published_at, rv.fetched_at) AS created_at, rv.updated_at,
           0::bigint AS views,
           (rv.thumbnail_key IS NOT NULL) AS has_thumbnail,
           (ra.preferred_username || '@' || ra.domain)::text AS channel_handle,
           ra.preferred_username AS channel_display_name,
           ''::text AS author_display_name,
           rv.duration_seconds,
           ra.domain,
           rv.watch_url,
           rv.stream_url,
           false AS is_sensitive,
           ''::text AS sensitive_reason,
           ''::text AS short_code
    FROM remote_videos rv
    JOIN remote_actors ra ON ra.actor_url = rv.remote_actor_url
    WHERE EXISTS (
          SELECT 1 FROM remote_channel_follows rcf
          WHERE rcf.user_id = sqlc.arg('follower_id')
            AND rcf.remote_actor_url = rv.remote_actor_url
            AND rcf.state = 'accepted'
      )
      AND NOT EXISTS (SELECT 1 FROM blocked_instances bi WHERE bi.domain = ra.domain)
      AND NOT EXISTS (SELECT 1 FROM remote_video_blocks rb WHERE rb.remote_video_id = rv.id)
      AND NOT EXISTS (
          SELECT 1 FROM muted_instances mi
          WHERE mi.muter_id = sqlc.arg('follower_id') AND mi.domain = ra.domain
      )
) AS feed
ORDER BY feed.created_at DESC, feed.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountSubscriptionVideos :one
-- How many rows ListSubscriptionVideos would return for the same follower,
-- ignoring pagination. The follow sets, mutes, blocks and instance
-- blocklist are all part of the predicate and are repeated verbatim.
SELECT count(*)::bigint
FROM (
    SELECT v.id,
           false AS remote,
           v.channel_id,
           v.title, v.description, v.privacy, v.state,
           v.created_at, v.updated_at,
           COALESCE(vc.views, 0)::bigint AS views,
           EXISTS (
               SELECT 1 FROM video_files f
               WHERE f.video_id = v.id AND f.kind = 'thumbnail'
           ) AS has_thumbnail,
           c.handle AS channel_handle, c.display_name AS channel_display_name,
           au.display_name AS author_display_name,
           vm.duration_seconds,
           ''::text AS domain,
           ''::text AS watch_url,
           NULL::text AS stream_url,
           v.is_sensitive,
           v.sensitive_reason
    FROM videos v
    JOIN channels c ON c.id = v.channel_id
    JOIN users au ON au.id = c.owner_id
    LEFT JOIN video_view_counts vc ON vc.video_id = v.id
    LEFT JOIN video_metadata vm ON vm.video_id = v.id
    WHERE v.privacy = 'public' AND v.state = 'published'
      AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
      AND NOT EXISTS (
          SELECT 1 FROM muted_accounts m
          WHERE m.muter_id = sqlc.arg('follower_id') AND m.muted_id = c.owner_id
      )
      -- §13: owners the follower has BLOCKED are hidden too.
      AND NOT EXISTS (
          SELECT 1 FROM user_blocks ub
          WHERE ub.blocker_id = sqlc.arg('follower_id') AND ub.blocked_id = c.owner_id
      )
      AND v.channel_id IN (
          SELECT channel_id FROM channel_follows WHERE follower_id = sqlc.arg('follower_id')
      )
    UNION ALL
    SELECT rv.id,
           true AS remote,
           '00000000-0000-0000-0000-000000000000'::uuid AS channel_id,
           rv.title, rv.description, ''::text AS privacy, ''::text AS state,
           COALESCE(rv.published_at, rv.fetched_at) AS created_at, rv.updated_at,
           0::bigint AS views,
           (rv.thumbnail_key IS NOT NULL) AS has_thumbnail,
           (ra.preferred_username || '@' || ra.domain)::text AS channel_handle,
           ra.preferred_username AS channel_display_name,
           ''::text AS author_display_name,
           rv.duration_seconds,
           ra.domain,
           rv.watch_url,
           rv.stream_url,
           false AS is_sensitive,
           ''::text AS sensitive_reason
    FROM remote_videos rv
    JOIN remote_actors ra ON ra.actor_url = rv.remote_actor_url
    WHERE EXISTS (
          SELECT 1 FROM remote_channel_follows rcf
          WHERE rcf.user_id = sqlc.arg('follower_id')
            AND rcf.remote_actor_url = rv.remote_actor_url
            AND rcf.state = 'accepted'
      )
      AND NOT EXISTS (SELECT 1 FROM blocked_instances bi WHERE bi.domain = ra.domain)
      AND NOT EXISTS (SELECT 1 FROM remote_video_blocks rb WHERE rb.remote_video_id = rv.id)
      AND NOT EXISTS (
          SELECT 1 FROM muted_instances mi
          WHERE mi.muter_id = sqlc.arg('follower_id') AND mi.domain = ra.domain
      )
) AS feed;

-- name: SearchPublicVideos :many
-- Public, published title search with discovery-card data. A local video also
-- matches when one of its (lowercased) tags contains the query substring;
-- ranking stays title-similarity first, so tag-only matches sort later.
-- Ingested remote videos are UNIONed in by title match (remote-content §4),
-- flagged remote with origin domain + watch/stream URLs; blocked instances
-- hide them for everyone and a signed-in viewer's instance mutes hide them too.
--
-- Sorting follows ListAdminVideos: sqlc cannot parameterise ORDER BY, so each
-- accepted ordering is a CASE branch over the bound `sort` argument and a branch
-- that does not match evaluates to NULL for every row, which is a no-op.
-- 'relevance' is the default and reproduces the previous fixed
-- `search_rank DESC, created_at DESC, id DESC` exactly. As in the admin list,
-- 'published_at' is created_at: local videos carry no separate published_at
-- column and the remote arm already projects COALESCE(published_at, fetched_at)
-- INTO created_at, so they are the same column by construction.
--
-- The duration and publish-window filters are applied at the OUTER level, over
-- the union, so they narrow local and remote rows by the same predicate. A row
-- whose duration is unknown (NULL — no probe has run, or a remote actor that
-- never advertised one) fails a duration bound rather than passing it: the
-- filter answers "provably within the range", and an unknown length cannot be
-- proven to be. The tag-set filters are local-only, like the single ?tag: remote
-- rows carry no local taxonomy, so any active tag set excludes them outright.
SELECT feed.id, feed.remote, feed.channel_id, feed.title, feed.description,
       feed.privacy, feed.state, feed.created_at, feed.updated_at, feed.views,
       feed.has_thumbnail, feed.channel_handle, feed.channel_display_name,
       feed.author_display_name,
       feed.duration_seconds, feed.domain, feed.watch_url, feed.stream_url,
       feed.is_sensitive, feed.sensitive_reason, feed.short_code
FROM (
    SELECT v.id,
           false AS remote,
           v.channel_id,
           v.title, v.description, v.privacy, v.state,
           v.created_at, v.updated_at,
           COALESCE(vc.views, 0)::bigint AS views,
           EXISTS (
               SELECT 1 FROM video_files f
               WHERE f.video_id = v.id AND f.kind = 'thumbnail'
           ) AS has_thumbnail,
           c.handle AS channel_handle, c.display_name AS channel_display_name,
           au.display_name AS author_display_name,
           vm.duration_seconds,
           ''::text AS domain,
           ''::text AS watch_url,
           NULL::text AS stream_url,
           v.is_sensitive,
           v.sensitive_reason,
           v.short_code,
           similarity(v.title, sqlc.arg('query')) AS search_rank
    FROM videos v
    JOIN channels c ON c.id = v.channel_id
    JOIN users au ON au.id = c.owner_id
    LEFT JOIN video_view_counts vc ON vc.video_id = v.id
    LEFT JOIN video_metadata vm ON vm.video_id = v.id
    WHERE v.privacy = 'public' AND v.state = 'published'
      AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
      AND NOT EXISTS (
          SELECT 1 FROM muted_accounts m
          WHERE m.muter_id = sqlc.narg('viewer_id') AND m.muted_id = c.owner_id
      )
      -- §13: owners the viewer has BLOCKED are hidden too (per-viewer, like mutes).
      AND NOT EXISTS (
          SELECT 1 FROM user_blocks ub
          WHERE ub.blocker_id = sqlc.narg('viewer_id') AND ub.blocked_id = c.owner_id
      )
      AND (v.title ILIKE '%' || sqlc.arg('query') || '%'
           OR EXISTS (
               SELECT 1 FROM video_tags t
               WHERE t.video_id = v.id AND t.tag ILIKE '%' || sqlc.arg('query') || '%'
           ))
      -- Optional facet filters (NULL = off), mirroring the feed: an exact
      -- free-form tag and the category/language/license taxonomy ids.
      AND (sqlc.narg('tag')::text IS NULL OR EXISTS (
          SELECT 1 FROM video_tags t WHERE t.video_id = v.id AND t.tag = sqlc.narg('tag')
      ))
      AND (sqlc.narg('category')::text IS NULL OR v.category = sqlc.narg('category'))
      AND (sqlc.narg('language')::text IS NULL OR v.language = sqlc.narg('language'))
      AND (sqlc.narg('license')::text IS NULL OR v.license = sqlc.narg('license'))
      -- Tag SETS (NULL = off). one_of is a disjunction; all_of demands every
      -- listed tag, counted DISTINCT so a caller repeating a tag cannot make the
      -- comparison unsatisfiable. Both arrive lowercased, like ?tag.
      AND (sqlc.narg('tags_one_of')::text[] IS NULL OR EXISTS (
          SELECT 1 FROM video_tags t
          WHERE t.video_id = v.id AND t.tag = ANY (sqlc.narg('tags_one_of')::text[])
      ))
      AND (sqlc.narg('tags_all_of')::text[] IS NULL OR (
          SELECT count(DISTINCT t.tag) FROM video_tags t
          WHERE t.video_id = v.id AND t.tag = ANY (sqlc.narg('tags_all_of')::text[])
      ) = (
          -- The DISTINCT count of the REQUESTED tags, not cardinality(): a
          -- caller sending ?tags_all_of=go,go would otherwise be asking for two
          -- matches of one tag, which the left side (also DISTINCT) can never
          -- reach, and the filter would silently match nothing.
          SELECT count(DISTINCT want) FROM unnest(sqlc.narg('tags_all_of')::text[]) AS want
      ))
      -- Unlisted owners (§16) are excluded from discovery; direct URLs still serve.
      AND NOT EXISTS (SELECT 1 FROM users u WHERE u.id = c.owner_id AND u.unlisted)
      -- Sensitive-content policy "hide" (instance-platform-info): flagged videos
      -- drop out of PUBLIC discovery only (owner/admin/direct reads unfiltered).
      AND (NOT sqlc.arg('hide_sensitive')::bool OR NOT v.is_sensitive)
    UNION ALL
    SELECT rv.id,
           true AS remote,
           '00000000-0000-0000-0000-000000000000'::uuid AS channel_id,
           rv.title, rv.description, ''::text AS privacy, ''::text AS state,
           COALESCE(rv.published_at, rv.fetched_at) AS created_at, rv.updated_at,
           0::bigint AS views,
           (rv.thumbnail_key IS NOT NULL) AS has_thumbnail,
           (ra.preferred_username || '@' || ra.domain)::text AS channel_handle,
           ra.preferred_username AS channel_display_name,
           ''::text AS author_display_name,
           rv.duration_seconds,
           ra.domain,
           rv.watch_url,
           rv.stream_url,
           false AS is_sensitive,
           ''::text AS sensitive_reason,
           ''::text AS short_code,
           similarity(rv.title, sqlc.arg('query')) AS search_rank
    FROM remote_videos rv
    JOIN remote_actors ra ON ra.actor_url = rv.remote_actor_url
    -- Remote videos carry no local taxonomy/tags, so any active facet filter
    -- excludes them (matching the main feed's behavior). The tag SETS are part
    -- of that rule for the same reason; duration and the publish window are not,
    -- because a remote row does carry both and is filtered on them below.
    WHERE sqlc.narg('tag')::text IS NULL
      AND sqlc.narg('category')::text IS NULL
      AND sqlc.narg('language')::text IS NULL
      AND sqlc.narg('license')::text IS NULL
      AND sqlc.narg('tags_one_of')::text[] IS NULL
      AND sqlc.narg('tags_all_of')::text[] IS NULL
      AND rv.title ILIKE '%' || sqlc.arg('query') || '%'
      AND NOT EXISTS (SELECT 1 FROM blocked_instances bi WHERE bi.domain = ra.domain)
      AND NOT EXISTS (SELECT 1 FROM remote_video_blocks rb WHERE rb.remote_video_id = rv.id)
      AND NOT EXISTS (
          SELECT 1 FROM muted_instances mi
          WHERE mi.muter_id = sqlc.narg('viewer_id') AND mi.domain = ra.domain
      )
) AS feed
WHERE (sqlc.narg('duration_min')::int IS NULL OR feed.duration_seconds >= sqlc.narg('duration_min')::int)
  AND (sqlc.narg('duration_max')::int IS NULL OR feed.duration_seconds <= sqlc.narg('duration_max')::int)
  AND (sqlc.narg('published_after')::timestamptz IS NULL OR feed.created_at >= sqlc.narg('published_after')::timestamptz)
  AND (sqlc.narg('published_before')::timestamptz IS NULL OR feed.created_at <= sqlc.narg('published_before')::timestamptz)
ORDER BY
    CASE WHEN sqlc.arg('sort')::text = 'relevance' THEN feed.search_rank END DESC,
    CASE WHEN sqlc.arg('sort')::text = 'published_at' THEN feed.created_at END ASC,
    CASE WHEN sqlc.arg('sort')::text = '-published_at' THEN feed.created_at END DESC,
    CASE WHEN sqlc.arg('sort')::text = 'views' THEN feed.views END ASC,
    CASE WHEN sqlc.arg('sort')::text = '-views' THEN feed.views END DESC,
    feed.created_at DESC, feed.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountSearchPublicVideos :one
-- How many rows SearchPublicVideos would return for the same query, filters
-- and viewer, ignoring pagination. The match predicates are repeated
-- verbatim; only the relevance ORDER BY is dropped, which cannot change a
-- count.
SELECT count(*)::bigint
FROM (
    SELECT v.id,
           false AS remote,
           v.channel_id,
           v.title, v.description, v.privacy, v.state,
           v.created_at, v.updated_at,
           COALESCE(vc.views, 0)::bigint AS views,
           EXISTS (
               SELECT 1 FROM video_files f
               WHERE f.video_id = v.id AND f.kind = 'thumbnail'
           ) AS has_thumbnail,
           c.handle AS channel_handle, c.display_name AS channel_display_name,
           au.display_name AS author_display_name,
           vm.duration_seconds,
           ''::text AS domain,
           ''::text AS watch_url,
           NULL::text AS stream_url,
           v.is_sensitive,
           v.sensitive_reason,
           similarity(v.title, sqlc.arg('query')) AS search_rank
    FROM videos v
    JOIN channels c ON c.id = v.channel_id
    JOIN users au ON au.id = c.owner_id
    LEFT JOIN video_view_counts vc ON vc.video_id = v.id
    LEFT JOIN video_metadata vm ON vm.video_id = v.id
    WHERE v.privacy = 'public' AND v.state = 'published'
      AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
      AND NOT EXISTS (
          SELECT 1 FROM muted_accounts m
          WHERE m.muter_id = sqlc.narg('viewer_id') AND m.muted_id = c.owner_id
      )
      -- §13: owners the viewer has BLOCKED are hidden too (per-viewer, like mutes).
      AND NOT EXISTS (
          SELECT 1 FROM user_blocks ub
          WHERE ub.blocker_id = sqlc.narg('viewer_id') AND ub.blocked_id = c.owner_id
      )
      AND (v.title ILIKE '%' || sqlc.arg('query') || '%'
           OR EXISTS (
               SELECT 1 FROM video_tags t
               WHERE t.video_id = v.id AND t.tag ILIKE '%' || sqlc.arg('query') || '%'
           ))
      -- Optional facet filters (NULL = off), mirroring the feed: an exact
      -- free-form tag and the category/language/license taxonomy ids.
      AND (sqlc.narg('tag')::text IS NULL OR EXISTS (
          SELECT 1 FROM video_tags t WHERE t.video_id = v.id AND t.tag = sqlc.narg('tag')
      ))
      AND (sqlc.narg('category')::text IS NULL OR v.category = sqlc.narg('category'))
      AND (sqlc.narg('language')::text IS NULL OR v.language = sqlc.narg('language'))
      AND (sqlc.narg('license')::text IS NULL OR v.license = sqlc.narg('license'))
      -- Tag SETS (NULL = off). one_of is a disjunction; all_of demands every
      -- listed tag, counted DISTINCT so a caller repeating a tag cannot make the
      -- comparison unsatisfiable. Both arrive lowercased, like ?tag.
      AND (sqlc.narg('tags_one_of')::text[] IS NULL OR EXISTS (
          SELECT 1 FROM video_tags t
          WHERE t.video_id = v.id AND t.tag = ANY (sqlc.narg('tags_one_of')::text[])
      ))
      AND (sqlc.narg('tags_all_of')::text[] IS NULL OR (
          SELECT count(DISTINCT t.tag) FROM video_tags t
          WHERE t.video_id = v.id AND t.tag = ANY (sqlc.narg('tags_all_of')::text[])
      ) = (
          -- The DISTINCT count of the REQUESTED tags, not cardinality(): a
          -- caller sending ?tags_all_of=go,go would otherwise be asking for two
          -- matches of one tag, which the left side (also DISTINCT) can never
          -- reach, and the filter would silently match nothing.
          SELECT count(DISTINCT want) FROM unnest(sqlc.narg('tags_all_of')::text[]) AS want
      ))
      -- Unlisted owners (§16) are excluded from discovery; direct URLs still serve.
      AND NOT EXISTS (SELECT 1 FROM users u WHERE u.id = c.owner_id AND u.unlisted)
      -- Sensitive-content policy "hide" (instance-platform-info): flagged videos
      -- drop out of PUBLIC discovery only (owner/admin/direct reads unfiltered).
      AND (NOT sqlc.arg('hide_sensitive')::bool OR NOT v.is_sensitive)
    UNION ALL
    SELECT rv.id,
           true AS remote,
           '00000000-0000-0000-0000-000000000000'::uuid AS channel_id,
           rv.title, rv.description, ''::text AS privacy, ''::text AS state,
           COALESCE(rv.published_at, rv.fetched_at) AS created_at, rv.updated_at,
           0::bigint AS views,
           (rv.thumbnail_key IS NOT NULL) AS has_thumbnail,
           (ra.preferred_username || '@' || ra.domain)::text AS channel_handle,
           ra.preferred_username AS channel_display_name,
           ''::text AS author_display_name,
           rv.duration_seconds,
           ra.domain,
           rv.watch_url,
           rv.stream_url,
           false AS is_sensitive,
           ''::text AS sensitive_reason,
           similarity(rv.title, sqlc.arg('query')) AS search_rank
    FROM remote_videos rv
    JOIN remote_actors ra ON ra.actor_url = rv.remote_actor_url
    -- Remote videos carry no local taxonomy/tags, so any active facet filter
    -- excludes them (matching the main feed's behavior). The tag SETS are part
    -- of that rule for the same reason; duration and the publish window are not,
    -- because a remote row does carry both and is filtered on them below.
    WHERE sqlc.narg('tag')::text IS NULL
      AND sqlc.narg('category')::text IS NULL
      AND sqlc.narg('language')::text IS NULL
      AND sqlc.narg('license')::text IS NULL
      AND sqlc.narg('tags_one_of')::text[] IS NULL
      AND sqlc.narg('tags_all_of')::text[] IS NULL
      AND rv.title ILIKE '%' || sqlc.arg('query') || '%'
      AND NOT EXISTS (SELECT 1 FROM blocked_instances bi WHERE bi.domain = ra.domain)
      AND NOT EXISTS (SELECT 1 FROM remote_video_blocks rb WHERE rb.remote_video_id = rv.id)
      AND NOT EXISTS (
          SELECT 1 FROM muted_instances mi
          WHERE mi.muter_id = sqlc.narg('viewer_id') AND mi.domain = ra.domain
      )
) AS feed
WHERE (sqlc.narg('duration_min')::int IS NULL OR feed.duration_seconds >= sqlc.narg('duration_min')::int)
  AND (sqlc.narg('duration_max')::int IS NULL OR feed.duration_seconds <= sqlc.narg('duration_max')::int)
  AND (sqlc.narg('published_after')::timestamptz IS NULL OR feed.created_at >= sqlc.narg('published_after')::timestamptz)
  AND (sqlc.narg('published_before')::timestamptz IS NULL OR feed.created_at <= sqlc.narg('published_before')::timestamptz);

-- name: ListPublicVideosByIDs :many
-- Hydrate a set of ranked video ids to discovery cards under the FULL canonical
-- predicate (search-service W4): public+published, not blocked, owner not
-- unlisted, per-viewer mutes/blocks, and the server-side hide_sensitive policy.
-- vidra-search returns ranked ids only (visibility-safe: the index stores static
-- eligibility, never per-viewer state); core hydrates + filters here and the Go
-- layer re-applies the search order. Rows come back in arbitrary order.
SELECT v.id, v.channel_id, v.title, v.description, v.privacy, v.state,
       v.created_at, v.updated_at,
       COALESCE(vc.views, 0)::bigint AS views,
       EXISTS (
           SELECT 1 FROM video_files f
           WHERE f.video_id = v.id AND f.kind = 'thumbnail'
       ) AS has_thumbnail,
       c.handle AS channel_handle, c.display_name AS channel_display_name,
       au.display_name AS author_display_name,
       vm.duration_seconds, v.is_sensitive, v.sensitive_reason, v.short_code
FROM videos v
JOIN channels c ON c.id = v.channel_id
JOIN users au ON au.id = c.owner_id
LEFT JOIN video_view_counts vc ON vc.video_id = v.id
LEFT JOIN video_metadata vm ON vm.video_id = v.id
WHERE v.id = ANY (sqlc.arg('ids')::uuid[])
  AND v.privacy = 'public' AND v.state = 'published'
  AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts m
      WHERE m.muter_id = sqlc.narg('viewer_id') AND m.muted_id = c.owner_id
  )
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks ub
      WHERE ub.blocker_id = sqlc.narg('viewer_id') AND ub.blocked_id = c.owner_id
  )
  AND NOT EXISTS (SELECT 1 FROM users u WHERE u.id = c.owner_id AND u.unlisted)
  AND (NOT sqlc.arg('hide_sensitive')::bool OR NOT v.is_sensitive);

-- name: ListRelatedVideosFallback :many
-- Server-side "related videos" fallback (search-service W4) when vidra-search is
-- unavailable or empty: same-channel + same-category candidates, excluding the
-- source video, under the full canonical predicate (public+published, not
-- blocked, owner not unlisted, per-viewer mutes/blocks, hide_sensitive). Ordered
-- same-channel first, then by all-time views, then newest — the server-side
-- version of the frontend's current related heuristic.
SELECT v.id, v.channel_id, v.title, v.description, v.privacy, v.state,
       v.created_at, v.updated_at,
       COALESCE(vc.views, 0)::bigint AS views,
       EXISTS (
           SELECT 1 FROM video_files f
           WHERE f.video_id = v.id AND f.kind = 'thumbnail'
       ) AS has_thumbnail,
       c.handle AS channel_handle, c.display_name AS channel_display_name,
       au.display_name AS author_display_name,
       vm.duration_seconds, v.is_sensitive, v.sensitive_reason, v.short_code,
       (v.channel_id = sqlc.arg('channel_id'))::bool AS same_channel
FROM videos v
JOIN channels c ON c.id = v.channel_id
JOIN users au ON au.id = c.owner_id
LEFT JOIN video_view_counts vc ON vc.video_id = v.id
LEFT JOIN video_metadata vm ON vm.video_id = v.id
WHERE v.privacy = 'public' AND v.state = 'published'
  AND v.id <> sqlc.arg('exclude_id')
  AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts m
      WHERE m.muter_id = sqlc.narg('viewer_id') AND m.muted_id = c.owner_id
  )
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks ub
      WHERE ub.blocker_id = sqlc.narg('viewer_id') AND ub.blocked_id = c.owner_id
  )
  AND NOT EXISTS (SELECT 1 FROM users u WHERE u.id = c.owner_id AND u.unlisted)
  AND (NOT sqlc.arg('hide_sensitive')::bool OR NOT v.is_sensitive)
  AND (
      v.channel_id = sqlc.arg('channel_id')
      OR (sqlc.narg('category')::text IS NOT NULL AND v.category = sqlc.narg('category'))
  )
ORDER BY same_channel DESC, views DESC, v.created_at DESC, v.id DESC
LIMIT sqlc.arg('result_limit');

-- name: UpdateVideo :one
UPDATE videos
SET title       = COALESCE(sqlc.narg('title'), title),
    description = COALESCE(sqlc.narg('description'), description),
    privacy     = COALESCE(sqlc.narg('privacy'), privacy),
    category    = COALESCE(sqlc.narg('category'), category),
    language    = COALESCE(sqlc.narg('language'), language),
    license     = COALESCE(sqlc.narg('license'), license),
    publish_at  = COALESCE(sqlc.narg('publish_at'), publish_at),
    is_sensitive = COALESCE(sqlc.narg('is_sensitive'), is_sensitive),
    sensitive_reason = COALESCE(sqlc.narg('sensitive_reason'), sensitive_reason),
    comments_policy  = COALESCE(sqlc.narg('comments_policy'), comments_policy),
    download_enabled = COALESCE(sqlc.narg('download_enabled'), download_enabled),
    publish_after_transcode = COALESCE(sqlc.narg('publish_after_transcode'), publish_after_transcode),
    originally_published_at = COALESCE(sqlc.narg('originally_published_at'), originally_published_at),
    updated_at  = now()
WHERE id = sqlc.arg('id')
RETURNING id, channel_id, title, description, privacy, state, created_at, updated_at, category, language, license, publish_at, embed_privacy, embed_allowed_domains, is_sensitive, comments_policy, download_enabled, publish_after_transcode, pinned_comment_id, sensitive_reason, originally_published_at, short_code, peertube_uuid;

-- name: SetVideoState :one
UPDATE videos
SET state      = sqlc.arg('state'),
    updated_at = now()
WHERE id = sqlc.arg('id')
RETURNING id, channel_id, title, description, privacy, state, created_at, updated_at, category, language, license, publish_at, embed_privacy, embed_allowed_domains, is_sensitive, comments_policy, download_enabled, publish_after_transcode, pinned_comment_id, sensitive_reason, originally_published_at, short_code, peertube_uuid;

-- name: SetVideoPinnedComment :exec
-- Set (or clear, when NULL) a video's pinned comment (YouTube-style creator pin,
-- 0099). The single column holds at most one pin, so setting it atomically
-- replaces any prior pin. Local metadata only: it deliberately does NOT bump
-- updated_at (which the transcoding-hold sweeper reads as the hold-start time)
-- and never federates. Authorized in the HTTP layer (video owner/editor/staff).
UPDATE videos
SET pinned_comment_id = sqlc.narg('pinned_comment_id')
WHERE id = sqlc.arg('video_id');

-- name: PublishTranscodingVideo :execrows
-- The publish-after-transcode release CAS (0098): flips a HELD video to
-- published only while it is still in the 'transcoding' state, so concurrent
-- release triggers (completion hook, terminal-failure hook, stuck sweeper)
-- transition it exactly once — the caller fires the publish hooks only when the
-- returned row count says this call won the transition.
UPDATE videos
SET state      = 'published',
    updated_at = now()
WHERE id = $1 AND state = 'transcoding';

-- name: ListDueScheduledVideos :many
-- Videos whose scheduled publish time has arrived, joined with their stored
-- original (a scheduled video always has one — it went through processing).
-- The sweeper feeds each row through the same publish transition Process uses.
SELECT v.id, f.storage_key
FROM videos v
JOIN video_files f ON f.video_id = v.id AND f.kind = 'original'
WHERE v.state = 'scheduled' AND v.publish_at <= now()
ORDER BY v.publish_at, v.id
LIMIT $1;

-- name: ListStuckTranscodingVideos :many
-- Videos stuck in the publish-after-transcode hold past the safety timeout (a
-- crashed worker or a lost job): the release sweeper publishes them anyway (the
-- retained original is playable — hiding a video forever is worse). updated_at is
-- the hold-start moment. Oldest hold first.
SELECT v.id
FROM videos v
WHERE v.state = 'transcoding' AND v.updated_at < sqlc.arg('cutoff')
ORDER BY v.updated_at, v.id
LIMIT sqlc.arg('result_limit');

-- name: UploadRequiresQuarantine :one
-- Whether a finished upload of this video must park in 'quarantined' instead of
-- publishing (product-decisions.md §11): true when the owning account is a
-- non-privileged user (role 'user') without the admin-granted bypass. Only
-- consulted when the QUARANTINE_NEW_UPLOADS instance setting is on.
SELECT (u.role = 'user' AND NOT u.bypass_quarantine)::bool AS requires_quarantine
FROM videos v
JOIN channels c ON c.id = v.channel_id
JOIN users u ON u.id = c.owner_id
WHERE v.id = $1;

-- name: ListQuarantinedVideos :many
-- The moderation quarantine queue: quarantined videos newest first, with the
-- owning channel + account so a moderator can judge and follow up.
SELECT v.id, v.title, v.privacy, v.state, v.created_at,
       c.handle AS channel_handle, c.display_name AS channel_display_name,
       u.username AS owner_username
FROM videos v
JOIN channels c ON c.id = v.channel_id
JOIN users u ON u.id = c.owner_id
WHERE v.state = 'quarantined'
ORDER BY v.created_at DESC, v.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountQuarantinedVideos :one
-- How many rows ListQuarantinedVideos would return, ignoring pagination. The
-- channels/users JOINs are part of the predicate and the state filter must stay
-- identical.
SELECT count(*)::bigint
FROM videos v
JOIN channels c ON c.id = v.channel_id
JOIN users u ON u.id = c.owner_id
WHERE v.state = 'quarantined';

-- name: DeleteVideo :exec
DELETE FROM videos WHERE id = $1;

-- name: ListAdminVideos :many
-- The moderation inventory includes both locally hosted videos and federated
-- metadata rows. Local file facts are derived from the authoritative media
-- tables; remote rows never pretend to own files.
--
-- Sorting: sqlc cannot parameterise ORDER BY, so each supported ordering gets a
-- CASE branch over the bound `sort` argument — a branch that does not match
-- evaluates to NULL for every row and is therefore a no-op. `id` is the final
-- tiebreaker on every branch so a page boundary can never duplicate or drop a
-- row. The default '-created_at' reproduces the previous fixed
-- `created_at DESC, id DESC` exactly. 'published_at' is accepted as an alias of
-- 'created_at': videos carry no separate published_at column, and the remote arm
-- already projects COALESCE(published_at, fetched_at) INTO created_at, so they
-- are the same column by construction.
--
-- Filtering: every predicate is optional (NULL/'all' = off). states/privacies
-- are arrays so the admin UI can check several boxes at once. The file-type
-- filters are tri-state on purpose — absent means "all", not "false", or
-- "videos with no HLS" would be unexpressible.
SELECT inventory.*
FROM (
    SELECT v.id, v.title, v.privacy, v.state,
           c.handle AS channel_handle, c.display_name AS channel_display_name,
           COALESCE(vc.views, 0)::bigint AS views,
           v.created_at,
           vm.duration_seconds,
           true AS is_local,
           ''::text AS origin_domain,
           ''::text AS watch_url,
           v.is_sensitive,
           EXISTS (SELECT 1 FROM import_jobs ij WHERE ij.video_id = v.id) AS external_link,
           EXISTS (SELECT 1 FROM video_files f WHERE f.video_id = v.id AND f.kind = 'thumbnail') AS has_thumbnail,
           EXISTS (SELECT 1 FROM video_files f WHERE f.video_id = v.id AND f.kind = 'original') AS has_original,
           (SELECT count(*)::int FROM video_renditions r WHERE r.video_id = v.id) AS hls_count,
           (SELECT count(*)::int FROM video_files f WHERE f.video_id = v.id AND f.kind IN ('rendition', 'webm')) AS web_video_count,
           (
               COALESCE((SELECT sum(f.size_bytes) FROM video_files f WHERE f.video_id = v.id), 0) +
               COALESCE((SELECT sum(r.size_bytes) FROM video_renditions r WHERE r.video_id = v.id), 0)
           )::bigint AS size_bytes,
           EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id) AS blocked,
           -- The moderator's rejection note (0130) — the staff read-back of the
           -- prose the reject dialog collects. '' when the video was never
           -- rejected; the remote arm below is always '' (a federated row
           -- cannot be quarantined here).
           COALESCE((SELECT rj.note FROM video_rejections rj WHERE rj.video_id = v.id), '')::text AS moderation_note,
           (SELECT count(*) FROM video_ratings vr
             WHERE vr.video_id = v.id AND vr.rating = 'like')::bigint AS like_count,
           (SELECT count(*) FROM comments cm
             WHERE cm.video_id = v.id AND cm.deleted_at IS NULL)::bigint AS comment_count
    FROM videos v
    JOIN channels c ON c.id = v.channel_id
    LEFT JOIN video_view_counts vc ON vc.video_id = v.id
    LEFT JOIN video_metadata vm ON vm.video_id = v.id

    UNION ALL

    SELECT rv.id, rv.title, 'public'::text AS privacy, 'published'::text AS state,
           (ra.preferred_username || '@' || ra.domain)::text AS channel_handle,
           ra.preferred_username::text AS channel_display_name,
           0::bigint AS views,
           COALESCE(rv.published_at, rv.fetched_at) AS created_at,
           rv.duration_seconds,
           false AS is_local,
           ra.domain AS origin_domain,
           rv.watch_url,
           false AS is_sensitive,
           false AS external_link,
           (rv.thumbnail_key IS NOT NULL) AS has_thumbnail,
           false AS has_original,
           0::int AS hls_count,
           0::int AS web_video_count,
           0::bigint AS size_bytes,
           EXISTS (SELECT 1 FROM remote_video_blocks b WHERE b.remote_video_id = rv.id) AS blocked,
           ''::text AS moderation_note,
           0::bigint AS like_count,
           0::bigint AS comment_count
    FROM remote_videos rv
    JOIN remote_actors ra ON ra.actor_url = rv.remote_actor_url
) inventory
WHERE (sqlc.narg('query')::text IS NULL OR inventory.title ILIKE '%' || sqlc.narg('query') || '%')
  AND (sqlc.narg('states')::text[] IS NULL OR inventory.state = ANY(sqlc.narg('states')::text[]))
  AND (sqlc.narg('privacies')::text[] IS NULL OR inventory.privacy = ANY(sqlc.narg('privacies')::text[]))
  AND (sqlc.arg('scope')::text = 'all'
       OR (sqlc.arg('scope')::text = 'local' AND inventory.is_local)
       OR (sqlc.arg('scope')::text = 'remote' AND NOT inventory.is_local))
  AND (sqlc.narg('channel')::text IS NULL OR inventory.channel_handle = sqlc.narg('channel')::text)
  AND (sqlc.narg('published_after')::timestamptz IS NULL OR inventory.created_at >= sqlc.narg('published_after')::timestamptz)
  AND (sqlc.narg('published_before')::timestamptz IS NULL OR inventory.created_at <= sqlc.narg('published_before')::timestamptz)
  AND (sqlc.narg('has_original')::boolean IS NULL OR inventory.has_original = sqlc.narg('has_original')::boolean)
  AND (sqlc.narg('has_hls')::boolean IS NULL OR (inventory.hls_count > 0) = sqlc.narg('has_hls')::boolean)
  AND (sqlc.narg('has_web_files')::boolean IS NULL OR (inventory.web_video_count > 0) = sqlc.narg('has_web_files')::boolean)
ORDER BY
    CASE WHEN sqlc.arg('sort')::text IN ('created_at', 'published_at') THEN inventory.created_at END ASC,
    CASE WHEN sqlc.arg('sort')::text IN ('-created_at', '-published_at') THEN inventory.created_at END DESC,
    CASE WHEN sqlc.arg('sort')::text = 'views' THEN inventory.views END ASC,
    CASE WHEN sqlc.arg('sort')::text = '-views' THEN inventory.views END DESC,
    CASE WHEN sqlc.arg('sort')::text = 'duration' THEN inventory.duration_seconds END ASC,
    CASE WHEN sqlc.arg('sort')::text = '-duration' THEN inventory.duration_seconds END DESC,
    CASE WHEN sqlc.arg('sort')::text = 'title' THEN inventory.title END ASC,
    CASE WHEN sqlc.arg('sort')::text = '-title' THEN inventory.title END DESC,
    CASE WHEN sqlc.arg('sort')::text = 'state' THEN inventory.state END ASC,
    CASE WHEN sqlc.arg('sort')::text = '-state' THEN inventory.state END DESC,
    CASE WHEN sqlc.arg('sort')::text = 'likes' THEN inventory.like_count END ASC,
    CASE WHEN sqlc.arg('sort')::text = '-likes' THEN inventory.like_count END DESC,
    CASE WHEN sqlc.arg('sort')::text = 'comments' THEN inventory.comment_count END ASC,
    CASE WHEN sqlc.arg('sort')::text = '-comments' THEN inventory.comment_count END DESC,
    CASE WHEN sqlc.arg('sort')::text = 'size_bytes' THEN inventory.size_bytes END ASC,
    CASE WHEN sqlc.arg('sort')::text = '-size_bytes' THEN inventory.size_bytes END DESC,
    inventory.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountAdminVideos :one
-- How many rows ListAdminVideos would return for the same filters, ignoring
-- pagination. The inner UNION and the whole WHERE are repeated verbatim: this
-- is the number the admin "All videos" header shows, and it reported
-- len(page) — permanently "100" — before this query existed. The ORDER BY is
-- dropped; ordering cannot change a count.
SELECT count(*)::bigint
FROM (
    SELECT v.id, v.title, v.privacy, v.state,
           c.handle AS channel_handle, c.display_name AS channel_display_name,
           COALESCE(vc.views, 0)::bigint AS views,
           v.created_at,
           vm.duration_seconds,
           true AS is_local,
           ''::text AS origin_domain,
           ''::text AS watch_url,
           v.is_sensitive,
           EXISTS (SELECT 1 FROM import_jobs ij WHERE ij.video_id = v.id) AS external_link,
           EXISTS (SELECT 1 FROM video_files f WHERE f.video_id = v.id AND f.kind = 'thumbnail') AS has_thumbnail,
           EXISTS (SELECT 1 FROM video_files f WHERE f.video_id = v.id AND f.kind = 'original') AS has_original,
           (SELECT count(*)::int FROM video_renditions r WHERE r.video_id = v.id) AS hls_count,
           (SELECT count(*)::int FROM video_files f WHERE f.video_id = v.id AND f.kind IN ('rendition', 'webm')) AS web_video_count,
           (
               COALESCE((SELECT sum(f.size_bytes) FROM video_files f WHERE f.video_id = v.id), 0) +
               COALESCE((SELECT sum(r.size_bytes) FROM video_renditions r WHERE r.video_id = v.id), 0)
           )::bigint AS size_bytes,
           EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id) AS blocked,
           (SELECT count(*) FROM video_ratings vr
             WHERE vr.video_id = v.id AND vr.rating = 'like')::bigint AS like_count,
           (SELECT count(*) FROM comments cm
             WHERE cm.video_id = v.id AND cm.deleted_at IS NULL)::bigint AS comment_count
    FROM videos v
    JOIN channels c ON c.id = v.channel_id
    LEFT JOIN video_view_counts vc ON vc.video_id = v.id
    LEFT JOIN video_metadata vm ON vm.video_id = v.id

    UNION ALL

    SELECT rv.id, rv.title, 'public'::text AS privacy, 'published'::text AS state,
           (ra.preferred_username || '@' || ra.domain)::text AS channel_handle,
           ra.preferred_username::text AS channel_display_name,
           0::bigint AS views,
           COALESCE(rv.published_at, rv.fetched_at) AS created_at,
           rv.duration_seconds,
           false AS is_local,
           ra.domain AS origin_domain,
           rv.watch_url,
           false AS is_sensitive,
           false AS external_link,
           (rv.thumbnail_key IS NOT NULL) AS has_thumbnail,
           false AS has_original,
           0::int AS hls_count,
           0::int AS web_video_count,
           0::bigint AS size_bytes,
           EXISTS (SELECT 1 FROM remote_video_blocks b WHERE b.remote_video_id = rv.id) AS blocked,
           0::bigint AS like_count,
           0::bigint AS comment_count
    FROM remote_videos rv
    JOIN remote_actors ra ON ra.actor_url = rv.remote_actor_url
) inventory
WHERE (sqlc.narg('query')::text IS NULL OR inventory.title ILIKE '%' || sqlc.narg('query') || '%')
  AND (sqlc.narg('states')::text[] IS NULL OR inventory.state = ANY(sqlc.narg('states')::text[]))
  AND (sqlc.narg('privacies')::text[] IS NULL OR inventory.privacy = ANY(sqlc.narg('privacies')::text[]))
  AND (sqlc.arg('scope')::text = 'all'
       OR (sqlc.arg('scope')::text = 'local' AND inventory.is_local)
       OR (sqlc.arg('scope')::text = 'remote' AND NOT inventory.is_local))
  AND (sqlc.narg('channel')::text IS NULL OR inventory.channel_handle = sqlc.narg('channel')::text)
  AND (sqlc.narg('published_after')::timestamptz IS NULL OR inventory.created_at >= sqlc.narg('published_after')::timestamptz)
  AND (sqlc.narg('published_before')::timestamptz IS NULL OR inventory.created_at <= sqlc.narg('published_before')::timestamptz)
  AND (sqlc.narg('has_original')::boolean IS NULL OR inventory.has_original = sqlc.narg('has_original')::boolean)
  AND (sqlc.narg('has_hls')::boolean IS NULL OR (inventory.hls_count > 0) = sqlc.narg('has_hls')::boolean)
  AND (sqlc.narg('has_web_files')::boolean IS NULL OR (inventory.web_video_count > 0) = sqlc.narg('has_web_files')::boolean);

-- name: GetVideoIDByShortCode :one
-- Resolve the public short code to a video id. Returns the id ONLY: the caller
-- funnels it through the same visibility check and detail assembly the
-- by-uuid endpoint uses, so private/unlisted/locked semantics cannot drift
-- between the two ways of naming one video.
SELECT id FROM videos WHERE short_code = $1;

-- name: GetVideoIDByLegacyUUID :one
-- Resolve a UUID that appeared in an OLD public URL to the video it now names.
-- Two namespaces reach this query and both are UUIDs, so one lookup serves both:
--   /videos/watch/{uuid} minted by this instance before /videos/{uuid}  -> videos.id
--   /w/{shortUUID} and /videos/watch/{uuid} from an IMPORTED PeerTube   -> peertube_uuid
-- ORDER BY prefers our own namespace, so the answer stays deterministic in the
-- (astronomically unlikely) event one instance's video id equals another's
-- imported source UUID.
SELECT id FROM videos
WHERE id = $1 OR peertube_uuid = $1
ORDER BY (id = $1) DESC
LIMIT 1;
