-- 0089 down: drop the per-video publish-policy columns.
ALTER TABLE videos
    DROP COLUMN IF EXISTS comments_policy,
    DROP COLUMN IF EXISTS download_enabled;
