-- name: CreateVideo :one
INSERT INTO videos (channel_id, title, description, privacy, category, language, license, publish_at, is_sensitive, comments_policy, download_enabled)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING id, channel_id, title, description, privacy, state, created_at, updated_at, category, language, license, publish_at, embed_privacy, embed_allowed_domains, is_sensitive, comments_policy, download_enabled;

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
SELECT id, title, description
FROM videos
WHERE channel_id = $1 AND privacy = 'public' AND state = 'published'
ORDER BY created_at DESC, id DESC
LIMIT $2 OFFSET $3;

-- name: GetVideoByID :one
SELECT v.id, v.channel_id, v.title, v.description, v.privacy, v.state, v.created_at, v.updated_at,
       v.category, v.language, v.license, v.publish_at, v.is_sensitive,
       v.comments_policy, v.download_enabled,
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
-- publish_at so the studio can badge scheduled videos.
SELECT v.id, v.channel_id, v.title, v.description, v.privacy, v.state,
       v.created_at, v.updated_at, v.publish_at,
       COALESCE(vc.views, 0)::bigint AS views,
       EXISTS (
           SELECT 1 FROM video_files f
           WHERE f.video_id = v.id AND f.kind = 'thumbnail'
       ) AS has_thumbnail,
       c.handle AS channel_handle, c.display_name AS channel_display_name,
       au.display_name AS author_display_name,
       vm.duration_seconds, v.is_sensitive
FROM videos v
JOIN channels c ON c.id = v.channel_id
JOIN users au ON au.id = c.owner_id
LEFT JOIN video_view_counts vc ON vc.video_id = v.id
LEFT JOIN video_metadata vm ON vm.video_id = v.id
WHERE v.channel_id = $1
ORDER BY v.created_at DESC;

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
       vm.duration_seconds, v.is_sensitive
FROM videos v
JOIN channels c ON c.id = v.channel_id
JOIN users au ON au.id = c.owner_id
LEFT JOIN video_view_counts vc ON vc.video_id = v.id
LEFT JOIN video_metadata vm ON vm.video_id = v.id
WHERE v.channel_id = $1 AND v.privacy = 'public' AND v.state = 'published'
  AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
ORDER BY v.created_at DESC;

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
       feed.is_sensitive
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
           v.is_sensitive
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
           false AS is_sensitive
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
       feed.is_sensitive
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
           v.is_sensitive
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
           false AS is_sensitive
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

-- name: SearchPublicVideos :many
-- Public, published title search with discovery-card data. A local video also
-- matches when one of its (lowercased) tags contains the query substring;
-- ranking stays title-similarity first, so tag-only matches sort later.
-- Ingested remote videos are UNIONed in by title match (remote-content §4),
-- flagged remote with origin domain + watch/stream URLs; blocked instances
-- hide them for everyone and a signed-in viewer's instance mutes hide them too.
SELECT feed.id, feed.remote, feed.channel_id, feed.title, feed.description,
       feed.privacy, feed.state, feed.created_at, feed.updated_at, feed.views,
       feed.has_thumbnail, feed.channel_handle, feed.channel_display_name,
       feed.author_display_name,
       feed.duration_seconds, feed.domain, feed.watch_url, feed.stream_url,
       feed.is_sensitive
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
      -- free-form tag and the category/language taxonomy ids.
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
           similarity(rv.title, sqlc.arg('query')) AS search_rank
    FROM remote_videos rv
    JOIN remote_actors ra ON ra.actor_url = rv.remote_actor_url
    -- Remote videos carry no local taxonomy/tags, so any active facet filter
    -- excludes them (matching the main feed's behavior).
    WHERE sqlc.narg('tag')::text IS NULL
      AND sqlc.narg('category')::text IS NULL
      AND sqlc.narg('language')::text IS NULL
      AND rv.title ILIKE '%' || sqlc.arg('query') || '%'
      AND NOT EXISTS (SELECT 1 FROM blocked_instances bi WHERE bi.domain = ra.domain)
      AND NOT EXISTS (SELECT 1 FROM remote_video_blocks rb WHERE rb.remote_video_id = rv.id)
      AND NOT EXISTS (
          SELECT 1 FROM muted_instances mi
          WHERE mi.muter_id = sqlc.narg('viewer_id') AND mi.domain = ra.domain
      )
) AS feed
ORDER BY feed.search_rank DESC, feed.created_at DESC, feed.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

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
       vm.duration_seconds, v.is_sensitive
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
       vm.duration_seconds, v.is_sensitive,
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
    comments_policy  = COALESCE(sqlc.narg('comments_policy'), comments_policy),
    download_enabled = COALESCE(sqlc.narg('download_enabled'), download_enabled),
    updated_at  = now()
WHERE id = sqlc.arg('id')
RETURNING id, channel_id, title, description, privacy, state, created_at, updated_at, category, language, license, publish_at, embed_privacy, embed_allowed_domains, is_sensitive, comments_policy, download_enabled;

-- name: SetVideoState :one
UPDATE videos
SET state      = sqlc.arg('state'),
    updated_at = now()
WHERE id = sqlc.arg('id')
RETURNING id, channel_id, title, description, privacy, state, created_at, updated_at, category, language, license, publish_at, embed_privacy, embed_allowed_domains, is_sensitive, comments_policy, download_enabled;

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

-- name: DeleteVideo :exec
DELETE FROM videos WHERE id = $1;

-- name: ListAdminVideos :many
-- The moderation inventory includes both locally hosted videos and federated
-- metadata rows. Local file facts are derived from the authoritative media
-- tables; remote rows never pretend to own files.
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
           EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id) AS blocked
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
           EXISTS (SELECT 1 FROM remote_video_blocks b WHERE b.remote_video_id = rv.id) AS blocked
    FROM remote_videos rv
    JOIN remote_actors ra ON ra.actor_url = rv.remote_actor_url
) inventory
WHERE (sqlc.narg('query')::text IS NULL OR inventory.title ILIKE '%' || sqlc.narg('query') || '%')
ORDER BY inventory.created_at DESC, inventory.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');
