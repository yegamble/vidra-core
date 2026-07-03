-- name: IncrementVideoViewDay :exec
-- The per-day rollup twin of IncrementVideoViews, keyed on the UTC day so the
-- series is timezone-stable. Runs on the same deduped-view write path.
INSERT INTO video_view_days (video_id, day, views)
VALUES ($1, (now() AT TIME ZONE 'utc')::date, 1)
ON CONFLICT (video_id, day) DO UPDATE
SET views = video_view_days.views + 1;

-- name: ListVideoViewDays :many
-- A video's recorded view days since a cutoff (missing days = zero; the
-- service zero-fills the series).
SELECT day, views
FROM video_view_days
WHERE video_id = $1 AND day >= sqlc.arg('since')::date
ORDER BY day;

-- name: ListChannelViewDays :many
-- A channel's view days aggregated across all its videos since a cutoff.
SELECT d.day, SUM(d.views)::bigint AS views
FROM video_view_days d
JOIN videos v ON v.id = d.video_id
WHERE v.channel_id = $1 AND d.day >= sqlc.arg('since')::date
GROUP BY d.day
ORDER BY d.day;

-- name: GetVideoEngagementTotals :one
-- One-shot totals for the owner stats view of a single video.
SELECT
    COALESCE((SELECT vc.views FROM video_view_counts vc WHERE vc.video_id = $1), 0)::bigint AS views,
    (SELECT COUNT(*) FROM video_ratings r WHERE r.video_id = $1 AND r.rating = 'like')::bigint    AS likes,
    (SELECT COUNT(*) FROM video_ratings r WHERE r.video_id = $1 AND r.rating = 'dislike')::bigint AS dislikes,
    (SELECT COUNT(*) FROM comments cm WHERE cm.video_id = $1)::bigint AS comments;

-- name: GetChannelEngagementTotals :one
-- One-shot totals for the owner stats view of a whole channel (all its videos,
-- any privacy/state — the owner sees their own full numbers).
SELECT
    COALESCE((SELECT SUM(vc.views)
                FROM video_view_counts vc
                JOIN videos v ON v.id = vc.video_id
               WHERE v.channel_id = $1), 0)::bigint AS views,
    (SELECT COUNT(*) FROM video_ratings r JOIN videos v ON v.id = r.video_id
      WHERE v.channel_id = $1 AND r.rating = 'like')::bigint AS likes,
    (SELECT COUNT(*) FROM video_ratings r JOIN videos v ON v.id = r.video_id
      WHERE v.channel_id = $1 AND r.rating = 'dislike')::bigint AS dislikes,
    (SELECT COUNT(*) FROM comments cm JOIN videos v ON v.id = cm.video_id
      WHERE v.channel_id = $1)::bigint AS comments,
    (SELECT COUNT(*) FROM videos WHERE channel_id = $1)::bigint AS videos;
